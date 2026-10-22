// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Wallet stores wallet credentials and the bearer token they obtain.
//
// Create a Wallet with NewWallet: one built as a struct literal has no
// credentials. A Wallet is safe for concurrent use: callers that need a new
// token share a single token request. A copy of a Wallet shares its token,
// and the token request in progress, with the original.
type Wallet struct {
	credentials *Credentials
	url         string
	urlErr      error // why url could not be built

	// HTTPClient sends the token requests of Token, GetBearer and
	// NewBearer. When nil, a client of the library's own on
	// http.DefaultTransport sends them. HTTPClient is never modified: the
	// token request goes through a copy that does not follow redirects. (An
	// Instance passes its own HTTPClient instead.) Set it before first use.
	HTTPClient *http.Client

	// state is what changes after NewWallet. It sits behind a pointer, so a
	// Wallet holds no lock by value: a copy shares the state, and copying
	// one is clean for go vet's copylocks check.
	state *walletState
}

// walletState is a Wallet's token and its token request in progress.
type walletState struct {
	mu     sync.Mutex
	bearer *Bearer      // the current token, if any; guarded by mu
	flight *tokenFlight // the token request in progress, if any; guarded by mu
}

// tokenFlight is one token request, shared by every caller waiting for it.
type tokenFlight struct {
	done   chan struct{} // closed once the fields below are set
	bearer *Bearer
	err    error

	// panicked is set when sending the request panicked, with the value it
	// panicked with in panicValue. Each caller still waiting panics with it.
	panicked   bool
	panicValue any
}

// errIncomplete is the error of a token request that neither returned nor
// panicked: its transport called runtime.Goexit, as t.Fatal does.
var errIncomplete = fmt.Errorf("%w: the token request did not complete", ErrToken)

// NewBearer gets a new bearer token from the token endpoint and keeps it,
// even if the current token is still valid. It joins a token request that
// is already in progress rather than starting a second one, and returns when
// the request ends or ctx does. NewBearer reports no error: when the request
// fails, the current token is kept. Token says why there is no token. If
// sending the request panics, NewBearer panics with the same value.
func (w *Wallet) NewBearer(ctx context.Context) {
	if w == nil || w.state == nil || ctx.Err() != nil {
		return
	}
	f := w.start(ctx, w.HTTPClient)
	select {
	case <-f.done:
		f.repanic()
	case <-ctx.Done():
	}
}

// GetBearer returns a valid bearer token, or "" when none can be obtained.
// Token says why.
func (w *Wallet) GetBearer(ctx context.Context) string {
	tok, _ := w.Token(ctx)
	return tok
}

// Token returns a valid bearer token. When there is none, or the current one
// is due for renewal (see Bearer.IsExpired), Token asks the token endpoint
// for a new one. Concurrent callers share one request, and each stops
// waiting when its own ctx ends. The request itself carries on, for up to
// 30 seconds, so that the other callers can still use its token: it keeps
// the values of the context of the caller that started it, but not its
// cancellation.
//
// Every error wraps ErrToken, and the cause when there is one. A failure is
// not remembered: the next call asks again. While the current token has not
// yet expired, Token returns it if its renewal fails, or if ctx ends first.
// If sending the token request panics, in a transport of yours for example,
// each caller waiting for it panics with the same value.
func (w *Wallet) Token(ctx context.Context) (string, error) {
	if w == nil {
		return "", fmt.Errorf("%w: nil Wallet", ErrToken)
	}
	return w.token(ctx, w.HTTPClient)
}

// token is Token, with the client that sends the token request.
func (w *Wallet) token(ctx context.Context, client *http.Client) (string, error) {
	s := w.state
	if s == nil {
		return "", fmt.Errorf("%w: the Wallet has no credentials (create it with NewWallet)", ErrToken)
	}
	tok, f, err := w.join(ctx, client)
	if f == nil {
		return tok, err
	}
	select {
	case <-f.done:
	case <-ctx.Done():
		if tok := s.unexpired(); tok != "" {
			return tok, nil
		}
		return "", fmt.Errorf("%w: %w", ErrToken, ctx.Err())
	}
	f.repanic()
	if f.err == nil {
		return f.bearer.Value, nil
	}
	// The renewal failed, but the current token may be inside its renewal
	// margin and still valid.
	if tok := s.unexpired(); tok != "" {
		return tok, nil
	}
	return "", f.err
}

// join returns the current token when it is not due for renewal. Otherwise
// it returns the token request to wait for, which it starts when none is in
// progress, unless ctx has ended: then it returns the current token if that
// has not yet expired, or else ctx's error.
//
// The lock is released by a deferred call, so that a panic, such as the one
// a nil ctx causes, cannot leave the Wallet locked.
func (w *Wallet) join(ctx context.Context, client *http.Client) (string, *tokenFlight, error) {
	s := w.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.bearer; b != nil && !b.IsExpired() {
		return b.Value, nil, nil
	}
	if err := ctx.Err(); err != nil {
		if tok := unexpired(s.bearer); tok != "" {
			return tok, nil, nil
		}
		return "", nil, fmt.Errorf("%w: %w", ErrToken, err)
	}
	return "", w.startLocked(ctx, client), nil
}

// start returns the token request in progress, or starts one.
func (w *Wallet) start(ctx context.Context, client *http.Client) *tokenFlight {
	s := w.state
	s.mu.Lock()
	defer s.mu.Unlock()
	return w.startLocked(ctx, client)
}

// startLocked returns the token request in progress, or starts one.
// w.state.mu must be held.
//
// The request runs on a goroutine of its own, bounded by requestTimeout,
// with a context that keeps ctx's values but not its cancellation, so that a
// caller that stops waiting does not fail the request for the others.
func (w *Wallet) startLocked(ctx context.Context, client *http.Client) *tokenFlight {
	s := w.state
	if s.flight != nil {
		return s.flight
	}
	ctx = context.WithoutCancel(ctx)
	f := &tokenFlight{done: make(chan struct{})}
	s.flight = f
	go w.run(ctx, client, f)
	return f
}

// run sends the token request of f and records its outcome. The bookkeeping
// is deferred, so that it is done even when the transport, which may be the
// caller's own code, panics or calls runtime.Goexit: the waiting callers are
// released, the next call starts a new request, and a panic is handed to
// the waiting callers, which panic with it in turn.
func (w *Wallet) run(ctx context.Context, client *http.Client, f *tokenFlight) {
	var b *Bearer
	err := errIncomplete
	defer func() {
		if p := recover(); p != nil {
			f.panicked, f.panicValue = true, p
		}
		s := w.state
		s.mu.Lock()
		if err == nil {
			s.bearer = b
		}
		s.flight = nil
		s.mu.Unlock()
		f.bearer, f.err = b, err
		close(f.done)
	}()
	b, err = w.fetch(ctx, client)
}

// repanic panics with the value that sending the request panicked with, if
// it panicked.
func (f *tokenFlight) repanic() {
	if f.panicked {
		panic(f.panicValue)
	}
}

// unexpired returns the current token if it has not yet expired, or else "".
func (s *walletState) unexpired() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return unexpired(s.bearer)
}

// unexpired returns b's token if it has not yet expired, or else "".
func unexpired(b *Bearer) string {
	if b != nil && b.Value != "" && time.Now().Before(b.ExpirationDate) {
		return b.Value
	}
	return ""
}

// fetch asks the token endpoint for a new bearer token. Every error wraps
// ErrToken and the cause.
func (w *Wallet) fetch(ctx context.Context, client *http.Client) (*Bearer, error) {
	b, err := w.requestToken(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrToken, err)
	}
	return b, nil
}

// requestToken posts the credentials to the token endpoint and checks the
// token it answers with.
func (w *Wallet) requestToken(ctx context.Context, client *http.Client) (*Bearer, error) {
	if w.urlErr != nil {
		return nil, w.urlErr
	}
	if w.credentials == nil {
		return nil, errors.New("the Wallet has no credentials (create it with NewWallet)")
	}
	data, err := w.credentials.ToJSON()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	body, _, err := send(ctx, withRedirectPolicy(client, refuseRedirect), req, maxTokenBodySize)
	if err != nil {
		return nil, err
	}
	b, err := NewBearerFromJSON(body)
	if err != nil {
		return nil, fmt.Errorf("decode the token response: %w", err)
	}
	if b.Value == "" {
		return nil, errors.New("the token response has no accessToken")
	}
	if b.ExpiresIn <= 0 {
		return nil, fmt.Errorf("the token response has expiresIn %d; want a positive number of seconds", b.ExpiresIn)
	}
	return b, nil
}

// Format implements fmt.Formatter: whatever the verb, a Wallet prints its
// token URL and its client id, never its secret or its token.
func (w Wallet) Format(f fmt.State, verb rune) {
	s := fmt.Sprintf("{url:%s credentials:<nil>}", w.url)
	gs := fmt.Sprintf("apiconnect.Wallet{url:%q, credentials:(*apiconnect.Credentials)(nil)}", w.url)
	if c := w.credentials; c != nil {
		s = fmt.Sprintf("{url:%s credentials:%s}", w.url, c.String())
		gs = fmt.Sprintf("apiconnect.Wallet{url:%q, credentials:&%s}", w.url, c.GoString())
	}
	formatRedacted(f, verb, s, gs)
}

// NewWallet creates a new instance of Wallet.
func NewWallet(proto, host, client, secret string, port int) *Wallet {
	u, err := buildURL(proto, host, port, "token")
	return &Wallet{
		url:         u,
		urlErr:      err,
		credentials: &Credentials{ClientID: client, Secret: secret},
		state:       new(walletState),
	}
}
