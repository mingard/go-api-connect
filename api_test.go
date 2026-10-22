// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

// Tests that need the API added in v1.2.0: Token, the HTTPClient and
// MaxBodySize fields, the Err values and the redaction methods. The tests
// that use only the v1.1.0 API are in defects_test.go, which also holds the
// helpers, and in guards_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	gotoken "go/token"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// setBearer gives w the token b, as if w had fetched it.
func setBearer(w *Wallet, b *Bearer) {
	w.state.mu.Lock()
	defer w.state.mu.Unlock()
	w.state.bearer = b
}

// D1: a caller whose context ends while it waits for the shared token
// request returns at once with its context's error; the request carries on
// for the caller that started it.
func TestD1_AWaiterWhoseContextEndsReturnsPromptly(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	started := make(chan struct{}, 1)
	var fetches atomic.Int32
	srv := fakeDADI(t, func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		writeToken(w, "t1", 3600)
	}, answer(`{}`))
	defer unblock() // before the server closes, which waits for the handler
	inst := newTestInstance(t, srv)

	type result struct {
		tok string
		err error
	}
	first := make(chan result, 1)
	go func() {
		tok, err := inst.Token(context.Background())
		first <- result{tok, err}
	}()
	<-started // the first caller's token request is in flight

	for _, tc := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		want error
	}{
		{"deadline", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 100*time.Millisecond)
		}, context.DeadlineExceeded},
		{"cancel", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(100*time.Millisecond, cancel)
			return ctx, cancel
		}, context.Canceled},
	} {
		ctx, cancel := tc.ctx()
		begin := time.Now()
		tok, err := inst.Token(ctx)
		elapsed := time.Since(begin)
		cancel()
		if tok != "" || !errors.Is(err, tc.want) || !errors.Is(err, ErrToken) {
			t.Errorf("%s: Token = %q, %v; want \"\" and an error wrapping ErrToken and %v", tc.name, tok, err, tc.want)
		}
		if elapsed > 2*time.Second {
			t.Errorf("%s: Token returned %v after it was called; want soon after its context ended", tc.name, elapsed)
		}
	}
	select {
	case r := <-first:
		t.Fatalf("the first caller returned %q, %v before the token endpoint answered", r.tok, r.err)
	default:
	}
	unblock()
	if r := <-first; r.tok != "t1" || r.err != nil {
		t.Errorf("first caller: Token = %q, %v; want t1", r.tok, r.err)
	}
	if n := fetches.Load(); n != 1 {
		t.Errorf("%d token requests; want 1, shared", n)
	}
}

// D1: a failed token request is not remembered: the next call asks again.
func TestD1_AFailedTokenRequestIsNotCached(t *testing.T) {
	var fetches atomic.Int32
	srv := fakeDADI(t, func(w http.ResponseWriter, _ *http.Request) {
		if fetches.Add(1) == 1 {
			http.Error(w, "warming up", http.StatusServiceUnavailable)
			return
		}
		writeToken(w, "t2", 3600)
	}, answer(`{}`))
	inst := newTestInstance(t, srv)
	if tok, err := inst.Token(context.Background()); tok != "" || !errors.Is(err, ErrToken) {
		t.Errorf("first Token = %q, %v; want \"\" and an error wrapping ErrToken", tok, err)
	}
	if tok, err := inst.Token(context.Background()); tok != "t2" || err != nil {
		t.Errorf("second Token = %q, %v; want t2", tok, err)
	}
	if n := fetches.Load(); n != 2 {
		t.Errorf("%d token requests; want 2", n)
	}
}

// D1, D5: when a renewal fails while the current token is inside its
// renewal margin but not yet expired, the current token is still used; once
// it has expired, the failure is an error.
func TestD1_AFailedRenewalKeepsATokenThatHasNotExpired(t *testing.T) {
	var fetches atomic.Int32
	srv := fakeDADI(t, func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		http.Error(w, "down", http.StatusServiceUnavailable)
	}, answer(`{}`))
	inst := newTestInstance(t, srv)
	ctx := context.Background()

	setBearer(inst.wallet, &Bearer{Value: "current", ExpiresIn: 3600, ExpirationDate: time.Now().Add(30 * time.Second)})
	if tok, err := inst.Token(ctx); tok != "current" || err != nil {
		t.Errorf("30 s before expiry: Token = %q, %v; want current", tok, err)
	}
	if n := fetches.Load(); n != 1 {
		t.Errorf("%d renewal attempts; want 1", n)
	}
	// A caller whose context has ended gets the current token too, and
	// starts no renewal.
	ended, cancel := context.WithCancel(ctx)
	cancel()
	if tok, err := inst.Token(ended); tok != "current" || err != nil {
		t.Errorf("30 s before expiry, with a context that has ended: Token = %q, %v; want current", tok, err)
	}
	if n := fetches.Load(); n != 1 {
		t.Errorf("%d renewal attempts; want 1, none for the ended context", n)
	}
	setBearer(inst.wallet, &Bearer{Value: "current", ExpiresIn: 3600, ExpirationDate: time.Now().Add(-time.Second)})
	if tok, err := inst.Token(ctx); tok != "" || !errors.Is(err, ErrToken) {
		t.Errorf("after expiry: Token = %q, %v; want \"\" and an error wrapping ErrToken", tok, err)
	}
	if tok, err := inst.Token(ended); tok != "" || !errors.Is(err, ErrToken) || !errors.Is(err, context.Canceled) {
		t.Errorf("after expiry, with a context that has ended: Token = %q, %v; want \"\" and an error wrapping ErrToken and context.Canceled", tok, err)
	}
}

// D1: a token request whose transport calls runtime.Goexit, as t.Fatal
// does, ends in an error wrapping ErrToken, and is not remembered.
func TestD1_ATokenRequestThatGoexitsIsAnError(t *testing.T) {
	srv := fakeDADI(t, issue("t", 3600), answer(`{}`))
	inst := newTestInstance(t, srv)
	inner := http.DefaultTransport
	var calls atomic.Int32
	inst.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			runtime.Goexit()
		}
		return inner.RoundTrip(r)
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if tok, err := inst.Token(ctx); tok != "" || !errors.Is(err, ErrToken) || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Token = %q, %v; want \"\" and an error wrapping ErrToken at once", tok, err)
	}
	if tok, err := inst.Token(ctx); tok != "t" || err != nil {
		t.Errorf("the next Token = %q, %v; want t", tok, err)
	}
}

// D3: the redirect policies themselves. (Written with the implementation:
// the functions did not exist before.)
func TestD3_TheRedirectPolicies(t *testing.T) {
	request := func(u string) *http.Request {
		t.Helper()
		r, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, tc := range []struct {
		from, to string
		ok       bool
	}{
		{"https://api.example.com:443/a", "https://api.example.com/b", true},
		{"https://api.example.com:8443/a", "https://API.example.com:8443/b", true},
		{"http://api.example.com/a", "http://api.example.com:80/b", true},
		{"http://api.example.com:8080/a", "https://api.example.com:8080/b", true},
		{"https://[::1]:8443/a", "https://[::1]:8443/b", true},
		{"https://api.example.com/a", "http://api.example.com/b", false},
		{"https://api.example.com:443/a", "http://api.example.com:443/b", false},
		{"https://api.example.com:8443/a", "https://api.example.com:9443/b", false},
		{"https://api.example.com/a", "https://other.example.com/b", false},
		{"http://api.example.com/a", "http://api.example.com.evil.test/b", false},
		{"http://api.example.com/a", "https://api.example.com/b", false}, // port 80 to 443
	} {
		err := sameOrigin(request(tc.to), []*http.Request{request(tc.from)})
		if (err == nil) != tc.ok || err != nil && !errors.Is(err, ErrRedirect) {
			t.Errorf("%s to %s: %v; want allowed %t", tc.from, tc.to, err, tc.ok)
		}
	}
	// Each hop is compared with the first request.
	a, b := request("https://api.example.com/a"), request("https://api.example.com/b")
	if err := sameOrigin(request("https://other.example.com/c"), []*http.Request{a, b}); !errors.Is(err, ErrRedirect) {
		t.Errorf("third hop to another host: %v; want ErrRedirect", err)
	}
	if err := refuseRedirect(b, []*http.Request{a}); !errors.Is(err, ErrRedirect) {
		t.Errorf("token policy: %v; want ErrRedirect", err)
	}
	// A hop that changes the method, as net/http does for a POST, PUT or
	// DELETE answered 301, 302 or 303, is refused; one that keeps it is not.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		first, err := http.NewRequest(method, "https://api.example.com/a", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := sameOrigin(request("https://api.example.com/b"), []*http.Request{first}); !errors.Is(err, ErrRedirect) {
			t.Errorf("%s sent again as GET: %v; want ErrRedirect", method, err)
		}
		again, err := http.NewRequest(method, "https://api.example.com/b", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := sameOrigin(again, []*http.Request{first}); err != nil {
			t.Errorf("%s sent again as %s (a 307 or 308): %v; want allowed", method, method, err)
		}
	}
	// With no CheckRedirect of the caller's, net/http's limit of 10 holds,
	// and stopping there is an error wrapping ErrRedirect.
	check := withRedirectPolicy(nil, sameOrigin).CheckRedirect
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = a
	}
	if err := check(b, via[:9]); err != nil {
		t.Errorf("10th redirect: %v; want allowed", err)
	}
	if err := check(b, via); !errors.Is(err, ErrRedirect) || !strings.Contains(fmt.Sprint(err), "10 redirects") {
		t.Errorf("11th redirect: %v; want stopped after 10, with an error wrapping ErrRedirect", err)
	}
	// A CheckRedirect of the caller's decides instead, as in net/http.
	mine := withRedirectPolicy(&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}, sameOrigin).CheckRedirect
	if err := check(b, append(via, via...)); err == nil {
		t.Error("21st redirect allowed with no CheckRedirect of the caller's; want stopped")
	}
	if err := mine(b, append(via, via...)); err != nil {
		t.Errorf("21st redirect with a CheckRedirect that allows it: %v; want allowed", err)
	}
}

// D3: a redirect loop stops after 10 redirects with an error wrapping
// ErrRedirect.
func TestD3_ARedirectLoopIsAnErrRedirect(t *testing.T) {
	var hops atomic.Int32
	srv := fakeDADI(t, issue("t", 3600), func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	})
	_, _, err := newTestInstance(t, srv).Do(context.Background(), &Get{Collection: "loop"}, nil)
	if !errors.Is(err, ErrRedirect) || !errors.As(err, new(*url.Error)) {
		t.Errorf("a redirect loop: Do = %v; want a *url.Error wrapping ErrRedirect", err)
	}
	if n := hops.Load(); n != 10 {
		t.Errorf("%d API requests; want 10: the first, then 9 redirects followed and the 10th refused", n)
	}
}

// D10: Get.ID asks for one document: its path is the collection's, then
// the ID, escaped as one segment. In 1.1.0 a Collection of "books/<id>" did
// this, and it still does.
func TestD10_GetWithAnIDFetchesOneDocument(t *testing.T) {
	var mu sync.Mutex
	var hit string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", issue("t", 3600))
	mux.HandleFunc("GET /{v}/{db}/{c}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hit = "collection " + strconv.Quote(r.PathValue("c")) + " " + r.URL.RawQuery
		mu.Unlock()
		io.WriteString(w, `{"results":[]}`)
	})
	mux.HandleFunc("GET /{v}/{db}/{c}/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hit = "document " + strconv.Quote(r.PathValue("c")) + " " + strconv.Quote(r.PathValue("id")) + " " + r.URL.RawQuery
		mu.Unlock()
		io.WriteString(w, `{"results":[{"_id":"abc123"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	inst := newTestInstance(t, srv)
	for _, tc := range []struct {
		get  *Get
		want string
	}{
		{&Get{Collection: "books", ID: "abc123", Fields: []string{"title"}},
			`document "books" "abc123" count=0&fields=%7B%22title%22%3A1%7D&page=0`},
		{&Get{Collection: "books", ID: "a/b?c"}, `document "books" "a/b?c" count=0&page=0`},
		{&Get{Collection: "books"}, `collection "books" count=0&page=0`},
		{&Get{Collection: "books/abc123"}, `document "books" "abc123" count=0&page=0`},
	} {
		mu.Lock()
		hit = ""
		mu.Unlock()
		if _, _, err := inst.Do(context.Background(), tc.get, nil); err != nil {
			t.Errorf("Get{Collection: %q, ID: %q}: %v", tc.get.Collection, tc.get.ID, err)
		}
		mu.Lock()
		if hit != tc.want {
			t.Errorf("Get{Collection: %q, ID: %q} reached %s; want %s", tc.get.Collection, tc.get.ID, hit, tc.want)
		}
		mu.Unlock()
	}
	g := &Get{Collection: "books", ID: "abc123", Metadata: true}
	if err := g.Initialize("https", "api.example.com", testProperty, "t", 443); err != nil {
		t.Fatal(err)
	}
	if b, err := g.MarshalBinary(); err != nil || string(b) != "https://api.example.com:443/1.0/library/books/abc123/count" {
		t.Errorf("Get with ID and Metadata: URL %s, %v; want https://api.example.com:443/1.0/library/books/abc123/count", b, err)
	}
}

// Every example in example_test.go is shown in the package documentation:
// go/doc, which pkg.go.dev uses, attaches ExampleT_M only to a method M, so
// an example named after a field is dropped.
func TestExamplesAreShownInTheDocumentation(t *testing.T) {
	fset := gotoken.NewFileSet()
	var files []*ast.File
	for _, f := range librarySources(t) {
		f, err := parser.ParseFile(fset, f.name, f.src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	ex, err := parser.ParseFile(fset, "example_test.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, ex)
	pkg, err := doc.NewFromFiles(fset, files, "github.com/mingard/go-api-connect")
	if err != nil {
		t.Fatal(err)
	}
	shown := map[string]bool{}
	add := func(exs []*doc.Example) {
		for _, e := range exs {
			shown["Example"+e.Name] = true
		}
	}
	add(pkg.Examples)
	for _, f := range pkg.Funcs {
		add(f.Examples)
	}
	for _, typ := range pkg.Types {
		add(typ.Examples)
		for _, f := range typ.Funcs {
			add(f.Examples)
		}
		for _, m := range typ.Methods {
			add(m.Examples)
		}
	}
	var written int
	for _, d := range ex.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && strings.HasPrefix(fn.Name.Name, "Example") {
			written++
			if !shown[fn.Name.Name] {
				t.Errorf("%s is not shown in the documentation: no exported function, type or method matches its name", fn.Name.Name)
			}
		}
	}
	if written == 0 {
		t.Error("no examples found in example_test.go")
	}
}

// D3, D16: refused redirects wrap ErrRedirect, the policy holds for a
// supplied client too, and the supplied client is not modified.
func TestD3_RefusedRedirectsWrapErrRedirect(t *testing.T) {
	var collected, otherHits atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		collected.Add(1)
		writeToken(w, "collector-token", 3600)
	}))
	defer collector.Close()
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherHits.Add(1)
		io.WriteString(w, `{}`)
	}))
	defer other.Close()

	var redirectToken atomic.Bool
	api := httptest.NewTLSServer(routeToken(
		func(w http.ResponseWriter, r *http.Request) {
			if redirectToken.Load() {
				http.Redirect(w, r, collector.URL+"/collect", http.StatusTemporaryRedirect)
				return
			}
			writeToken(w, "t", 3600)
		},
		func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
		}))
	defer api.Close()
	client := api.Client()
	before := *client
	newInst := func() *Instance {
		inst := newTestInstance(t, api)
		inst.HTTPClient = client
		return inst
	}

	redirectToken.Store(true)
	if tok, err := newInst().Token(context.Background()); tok != "" || !errors.Is(err, ErrRedirect) || !errors.Is(err, ErrToken) {
		t.Errorf("token redirect: Token = %q, %v; want an error wrapping ErrToken and ErrRedirect", tok, err)
	}
	redirectToken.Store(false)
	if _, _, err := newInst().Do(context.Background(), &Get{Collection: "books"}, nil); !errors.Is(err, ErrRedirect) {
		t.Errorf("redirect to another port: Do = %v; want an error wrapping ErrRedirect", err)
	}
	if c, o := collected.Load(), otherHits.Load(); c > 0 || o > 0 {
		t.Errorf("the redirect targets were sent %d and %d request(s); want none", c, o)
	}
	if client.CheckRedirect != nil || client.Jar != before.Jar || client.Timeout != before.Timeout ||
		reflect.ValueOf(client.Transport).Pointer() != reflect.ValueOf(before.Transport).Pointer() {
		t.Errorf("the supplied client was modified: %+v; was %+v", *client, before)
	}
}

// D3, D16: a supplied client's own CheckRedirect still runs for a redirect
// the library allows, and never sees one the library refuses.
func TestD3_ASuppliedClientsCheckRedirectStillApplies(t *testing.T) {
	errMine := errors.New("my policy: no redirects")
	var calls atomic.Int32
	api := httptest.NewTLSServer(routeToken(issue("t", 3600), func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/1.0/library/books":
			http.Redirect(w, r, "/1.0/library/moved", http.StatusFound)
		case "/1.0/library/away":
			http.Redirect(w, r, "http://127.0.0.1:1/away", http.StatusFound)
		default:
			io.WriteString(w, `{}`)
		}
	}))
	defer api.Close()
	client := api.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		calls.Add(1)
		return errMine
	}
	inst := newTestInstance(t, api)
	inst.HTTPClient = client

	if _, _, err := inst.Do(context.Background(), &Get{Collection: "books"}, nil); !errors.Is(err, errMine) {
		t.Errorf("same-origin redirect: Do = %v; want the client's own error", err)
	}
	if _, _, err := inst.Do(context.Background(), &Get{Collection: "away"}, nil); !errors.Is(err, ErrRedirect) {
		t.Errorf("redirect from https to http: Do = %v; want an error wrapping ErrRedirect", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("the client's CheckRedirect ran %d times; want 1 (the allowed redirect only)", n)
	}
}

// D5: Token returns the token that Do sends, and a Wallet uses its
// HTTPClient.
func TestD5_TokenReturnsTheTokenDoSends(t *testing.T) {
	var auth atomic.Value
	srv := fakeDADI(t, issue("t-ok", 3600), func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		io.WriteString(w, `{}`)
	})
	ctx := context.Background()
	inst := newTestInstance(t, srv)
	if tok, err := inst.Token(ctx); tok != "t-ok" || err != nil {
		t.Errorf("Instance.Token = %q, %v; want t-ok", tok, err)
	}
	if _, _, err := inst.Do(ctx, &Get{Collection: "books"}, nil); err != nil || auth.Load() != "Bearer t-ok" {
		t.Errorf("Do = %v with Authorization %q; want Bearer t-ok", err, auth.Load())
	}

	proto, host, port := hostPort(t, srv.URL)
	w := NewWallet(proto, host, testClientID, testSecret, port)
	var used atomic.Int32
	w.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used.Add(1)
		return http.DefaultTransport.RoundTrip(r)
	})}
	if tok, err := w.Token(ctx); tok != "t-ok" || err != nil {
		t.Errorf("Wallet.Token = %q, %v; want t-ok", tok, err)
	}
	if tok := w.GetBearer(ctx); tok != "t-ok" {
		t.Errorf("Wallet.GetBearer = %q; want t-ok", tok)
	}
	if n := used.Load(); n != 1 {
		t.Errorf("Wallet.HTTPClient sent %d token request(s); want 1", n)
	}
}

// D5: Token and Do say why there is no token, as an error that wraps
// ErrToken and the cause, and Do sends nothing.
func TestD5_TokenSaysWhyThereIsNoToken(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	for _, tc := range []struct {
		name  string
		url   string // a server with no handler, used when token is nil
		token http.HandlerFunc
		check func(error) bool
		what  string
	}{
		{name: "500", token: func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "down", http.StatusInternalServerError)
		}, check: func(err error) bool { return strings.Contains(err.Error(), "500") }, what: "names the status"},
		{name: "no accessToken", token: answer(`{"expiresIn":3600}`),
			check: func(err error) bool { return strings.Contains(err.Error(), "accessToken") }, what: "names accessToken"},
		{name: "expiresIn 0", token: answer(`{"accessToken":"abc","expiresIn":0}`),
			check: func(err error) bool { return strings.Contains(err.Error(), "expiresIn") }, what: "names expiresIn"},
		{name: "not JSON", token: answer(`<html>`),
			check: func(err error) bool { return errors.As(err, new(*json.SyntaxError)) }, what: "wraps the *json.SyntaxError"},
		{name: "connection refused", url: closedURL,
			check: func(err error) bool { return errors.As(err, new(*url.Error)) }, what: "wraps the *url.Error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			base := tc.url
			if tc.token != nil {
				base = fakeDADI(t, tc.token, func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					io.WriteString(w, `{}`)
				}).URL
			}
			proto, host, port := hostPort(t, base)
			ctx := context.Background()
			inst := New(proto, host, testProperty, testClientID, testSecret, port)
			wallet := NewWallet(proto, host, testClientID, testSecret, port)
			for _, call := range []struct {
				name string
				get  func() (string, error)
			}{
				{"Instance.Token", func() (string, error) { return inst.Token(ctx) }},
				{"Wallet.Token", func() (string, error) { return wallet.Token(ctx) }},
				{"Instance.Do", func() (string, error) {
					_, _, err := inst.Do(ctx, &Get{Collection: "books"}, nil)
					return "", err
				}},
			} {
				tok, err := call.get()
				if tok != "" || !errors.Is(err, ErrToken) || !tc.check(err) {
					t.Errorf("%s = %q, %v; want an error that wraps ErrToken and %s", call.name, tok, err, tc.what)
				}
			}
			if n := requests.Load(); n > 0 {
				t.Errorf("Do sent %d request(s) without a token", n)
			}
		})
	}
}

// D8: MaxBodySize on an Instance or an HTTPRequest caps the body; exactly the
// limit is fine, one byte more is ErrBodyTooLarge. Token responses are capped
// at 1 MiB.
func TestD8_MaxBodySizeIsEnforced(t *testing.T) {
	if DefaultMaxBodySize != 64<<20 {
		t.Errorf("DefaultMaxBodySize = %d; want 64 MiB", DefaultMaxBodySize)
	}
	// The collection names the body: "<bytes>-<chunked|length>".
	srv := fakeDADI(t, issue("t", 3600), func(w http.ResponseWriter, r *http.Request) {
		size, mode, _ := strings.Cut(path.Base(r.URL.Path), "-")
		n, _ := strconv.Atoi(size)
		body := bytes.Repeat([]byte("a"), n)
		if mode == "length" {
			w.Header().Set("Content-Length", size)
			w.Write(body)
			return
		}
		w.Write(body[:n/2])
		w.(http.Flusher).Flush() // no Content-Length: the rest goes chunked
		w.Write(body[n/2:])
	})
	ctx := context.Background()
	inst := newTestInstance(t, srv)
	inst.MaxBodySize = 1024
	for _, mode := range []string{"chunked", "length"} {
		if body, _, err := inst.Do(ctx, &Get{Collection: "1024-" + mode}, nil); err != nil || len(body) != 1024 {
			t.Errorf("%s, 1024 bytes with a 1024-byte limit: %d bytes, %v; want all of it", mode, len(body), err)
		}
		if body, _, err := inst.Do(ctx, &Get{Collection: "1025-" + mode}, nil); body != nil || !errors.Is(err, ErrBodyTooLarge) {
			t.Errorf("%s, 1025 bytes with a 1024-byte limit: %d bytes, %v; want ErrBodyTooLarge", mode, len(body), err)
		}
	}

	proto, host, port := hostPort(t, srv.URL)
	g := &Get{Collection: "11-length"}
	if err := g.Initialize(proto, host, testProperty, "t", port); err != nil {
		t.Fatal(err)
	}
	g.HTTPRequest.MaxBodySize = 10
	if _, _, err := g.Do(ctx); !errors.Is(err, ErrBodyTooLarge) {
		t.Errorf("HTTPRequest.MaxBodySize 10, 11 bytes: %v; want ErrBodyTooLarge", err)
	}

	big := fakeDADI(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"accessToken":"t","expiresIn":3600,"pad":"%s"}`, strings.Repeat("a", 1<<20))
	}, answer(`{}`))
	if tok, err := newTestInstance(t, big).Token(ctx); tok != "" || !errors.Is(err, ErrBodyTooLarge) || !errors.Is(err, ErrToken) {
		t.Errorf("a token response over 1 MiB: Token = %q, %v; want ErrToken and ErrBodyTooLarge", tok, err)
	}
}

// D12: requests that were not initialized, and an Instance or Wallet with no
// credentials, return errors that can be tested with errors.Is.
func TestD12_ErrorsInsteadOfPanics(t *testing.T) {
	ctx := context.Background()
	for _, r := range []Request{&Get{}, &Post{}, &Put{}, &Delete{}} {
		var err error
		if !mustNotPanic(t, fmt.Sprintf("%T.MarshalBinary", r), func() { _, err = r.MarshalBinary() }) && !errors.Is(err, ErrNotInitialized) {
			t.Errorf("%T.MarshalBinary = %v; want ErrNotInitialized", r, err)
		}
		if !mustNotPanic(t, fmt.Sprintf("%T.Do", r), func() { _, _, err = r.Do(ctx) }) && !errors.Is(err, ErrNotInitialized) {
			t.Errorf("%T.Do = %v; want ErrNotInitialized", r, err)
		}
	}
	var err error
	if !mustNotPanic(t, "HTTPRequest.Do", func() { _, _, err = (&HTTPRequest{}).Do(ctx) }) && !errors.Is(err, ErrNotInitialized) {
		t.Errorf("HTTPRequest{}.Do = %v; want ErrNotInitialized", err)
	}

	var nilInstance *Instance
	var nilWallet *Wallet
	for _, tc := range []struct {
		name  string
		token func(context.Context) (string, error)
	}{
		{"Instance literal", (&Instance{Proto: "http", Hostname: "127.0.0.1", Port: 1}).Token},
		{"nil *Instance", nilInstance.Token},
		{"Wallet literal", (&Wallet{}).Token},
		{"nil *Wallet", nilWallet.Token},
	} {
		var tok string
		if !mustNotPanic(t, tc.name+" Token", func() { tok, err = tc.token(ctx) }) && (tok != "" || !errors.Is(err, ErrToken)) {
			t.Errorf("%s: Token = %q, %v; want an error wrapping ErrToken", tc.name, tok, err)
		}
	}
}

// D16: a supplied client sends the token and API requests and is not
// modified.
func TestD16_ASuppliedClientIsUsedAndNotModified(t *testing.T) {
	api := httptest.NewTLSServer(routeToken(issue("t", 3600), answer(`{}`)))
	defer api.Close()
	inner := api.Client().Transport
	var used atomic.Int32
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			used.Add(1)
			return inner.RoundTrip(r)
		}),
		Timeout: 10 * time.Second,
	}
	before := *client
	inst := newTestInstance(t, api)
	inst.HTTPClient = client
	if _, _, err := inst.Do(context.Background(), &Get{Collection: "books"}, nil); err != nil {
		t.Errorf("Do: %v", err)
	}
	if n := used.Load(); n != 2 {
		t.Errorf("the supplied client sent %d request(s); want 2 (token and API)", n)
	}
	if client.CheckRedirect != nil || client.Jar != before.Jar || client.Timeout != before.Timeout ||
		reflect.ValueOf(client.Transport).Pointer() != reflect.ValueOf(before.Transport).Pointer() {
		t.Errorf("the supplied client was modified")
	}
}

// D18: the redaction methods themselves.
func TestD18_TheRedactionMethods(t *testing.T) {
	c := Credentials{ClientID: testClientID, Secret: testSecret}
	b := Bearer{ExpiresIn: 3600, ExpirationDate: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), Value: "tok-value-123"}
	for _, tc := range []struct{ name, got, want string }{
		{"Credentials.String", c.String(), "{ClientID:client-123 Secret:[redacted]}"},
		{"Credentials.GoString", c.GoString(), `apiconnect.Credentials{ClientID:"client-123", Secret:"[redacted]"}`},
		{"Credentials.LogValue", c.LogValue().String(), "[clientId=client-123 secret=[redacted]]"},
		{"Credentials.String with no secret", Credentials{ClientID: "id"}.String(), "{ClientID:id Secret:}"},
		{"Bearer.String", b.String(), "{ExpiresIn:3600 ExpirationDate:2026-10-22T12:00:00Z Value:[redacted]}"},
		{"Bearer.GoString", b.GoString(),
			`apiconnect.Bearer{ExpiresIn:3600, ExpirationDate:time.Date(2026, time.October, 22, 12, 0, 0, 0, time.UTC), Value:"[redacted]"}`},
		{"Bearer.LogValue", b.LogValue().String(), "[expiresIn=3600 expirationDate=2026-10-22 12:00:00 +0000 UTC accessToken=[redacted]]"},
		{"Bearer.String with no token", Bearer{}.String(), "{ExpiresIn:0 ExpirationDate:0001-01-01T00:00:00Z Value:}"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s =\n%s\nwant\n%s", tc.name, tc.got, tc.want)
		}
	}
	if k := c.LogValue().Kind(); k != slog.KindGroup {
		t.Errorf("Credentials.LogValue kind %v; want Group", k)
	}
	if k := b.LogValue().Kind(); k != slog.KindGroup {
		t.Errorf("Bearer.LogValue kind %v; want Group", k)
	}
}
