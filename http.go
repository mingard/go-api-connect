// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultMaxBodySize is the largest response body, in bytes, that is read
// when no MaxBodySize is set: 64 MiB.
const DefaultMaxBodySize int64 = 64 << 20

const (
	// maxTokenBodySize caps the token response, a small JSON object: 1 MiB.
	maxTokenBodySize = 1 << 20

	// maxDrainSize is how much of an unread response body is read before
	// the body is closed, so that its connection can be reused.
	maxDrainSize = 64 << 10

	// maxDrainTime is how long that read may take: as long as net/http
	// itself spends draining a body that is closed unread.
	maxDrainTime = 50 * time.Millisecond

	// maxRedirects is the most redirects a request follows, as in net/http.
	maxRedirects = 10
)

// HTTPRequest stores http request data.
type HTTPRequest struct {
	Method  string
	URL     string
	Payload io.Reader
	Query   map[string]string
	Req     *http.Request
	Params  url.Values

	// Client sends the request. When nil, a client of the library's own on
	// http.DefaultTransport sends it; http.DefaultClient is never used.
	// Client is never modified: the request goes through a copy whose
	// redirect policy refuses a move from https to another scheme, any move
	// to another host or port, and any change of method, and then applies
	// Client's own CheckRedirect, which then decides how many redirects to
	// follow. Without one, the limit is net/http's 10.
	Client *http.Client

	// MaxBodySize is the largest response body, in bytes, that Do reads. A
	// longer body is an error wrapping ErrBodyTooLarge, never a truncated
	// body. Zero or less means DefaultMaxBodySize.
	MaxBodySize int64
}

// Do executes a request.
//
// A response other than 200, 204 or 304 is an error, and a 204 or 304 has no
// body. Do returns ErrNotInitialized when Initialize has not succeeded.
func (h *HTTPRequest) Do(ctx context.Context) ([]byte, *http.Header, error) {
	if h == nil || h.Req == nil {
		return nil, nil, ErrNotInitialized
	}
	if len(h.Params) > 0 {
		h.Req.URL.RawQuery = h.Params.Encode()
	}
	return send(ctx, withRedirectPolicy(h.Client, sameOrigin), h.Req, h.MaxBodySize)
}

// SetParam sets URL parameters.
func (h *HTTPRequest) SetParam(k, v string) {
	h.Params.Set(k, v)
}

// SetHeader sets a header parameter.
func (h *HTTPRequest) SetHeader(k, v string) {
	h.Req.Header.Set(k, v)
}

// Initialize prepares the HTTP request. A URL that does not parse is an
// error, and Req is left as it was.
func (h *HTTPRequest) Initialize() error {
	req, err := http.NewRequest(h.Method, h.URL, h.Payload)
	if err != nil {
		return err
	}
	h.Req = req
	h.Params = req.URL.Query()
	return nil
}

// send sends req through client and reads the response body, at most limit
// bytes (DefaultMaxBodySize when limit is zero or less).
func send(ctx context.Context, client *http.Client, req *http.Request, limit int64) ([]byte, *http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second*requestTimeout)
	defer cancel()

	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, nil, err
	}
	// Whatever the outcome, close the body, after reading what is left of a
	// short one, so that the connection goes back to the pool.
	defer drainAndClose(resp, cancel)

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNoContent, http.StatusNotModified:
		// No body: DADI API answers 204 to a DELETE unless its feedback
		// setting is on, and 304 to a request whose ETag still matches.
		return nil, &resp.Header, nil
	default:
		return nil, nil, fmt.Errorf("API request failed with code %d", resp.StatusCode)
	}

	body, err := readBody(resp, limit)
	return body, &resp.Header, err
}

// readBody reads resp's body. A body longer than limit bytes (or
// DefaultMaxBodySize when limit is zero or less) is an error wrapping
// ErrBodyTooLarge, and none of it is returned.
func readBody(resp *http.Response, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = DefaultMaxBodySize
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("%w: its Content-Length, %d bytes, is over the %d-byte limit",
			ErrBodyTooLarge, resp.ContentLength, limit)
	}
	var r io.Reader = resp.Body
	if limit < math.MaxInt64 {
		// One byte more than the limit tells a body at the limit from one
		// over it.
		r = io.LimitReader(resp.Body, limit+1)
	}
	body, err := io.ReadAll(r)
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: it is over the %d-byte limit", ErrBodyTooLarge, limit)
	}
	return body, err
}

// drainAndClose closes resp's body. First it reads what is left of a short
// body, up to maxDrainSize bytes and for at most maxDrainTime, so that its
// connection goes back to the pool before the next request. A body that is
// still arriving after maxDrainTime is given up: cancel ends its request,
// and with it the connection, so that a stalled body cannot hold the
// caller. A body known to be longer than maxDrainSize is closed unread.
func drainAndClose(resp *http.Response, cancel context.CancelFunc) {
	if resp.ContentLength <= maxDrainSize {
		stop := time.AfterFunc(maxDrainTime, cancel)
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainSize))
		stop.Stop()
	}
	_ = resp.Body.Close()
}

// Format implements fmt.Formatter: whatever the verb, an HTTPRequest prints
// its method and URL, never its headers, so never the bearer token in its
// Authorization header.
func (h HTTPRequest) Format(f fmt.State, verb rune) {
	formatRedacted(f, verb,
		fmt.Sprintf("{Method:%s URL:%s}", h.Method, h.URL),
		fmt.Sprintf("apiconnect.HTTPRequest{Method:%q, URL:%q}", h.Method, h.URL))
}

// redirectPolicy decides whether a request may follow a redirect to req; via
// holds the requests made so far, the first one first.
type redirectPolicy func(req *http.Request, via []*http.Request) error

// withRedirectPolicy returns a copy of c, or a new client on
// http.DefaultTransport when c is nil, whose CheckRedirect applies policy
// first and then c's own CheckRedirect, or else net/http's limit of 10
// redirects, as an error wrapping ErrRedirect. c itself is not modified.
func withRedirectPolicy(c *http.Client, policy redirectPolicy) *http.Client {
	var cp http.Client
	if c != nil {
		cp = *c
	}
	next := cp.CheckRedirect
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := policy(req, via); err != nil {
			return err
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= maxRedirects {
			return fmt.Errorf("%w: stopped after %d redirects", ErrRedirect, maxRedirects)
		}
		return nil
	}
	return &cp
}

// refuseRedirect is the redirect policy of the token request, which carries
// the client secret: it never follows a redirect.
func refuseRedirect(*http.Request, []*http.Request) error {
	return fmt.Errorf("%w: the token request does not follow redirects", ErrRedirect)
}

// sameOrigin is the redirect policy of API requests, which carry the bearer
// token: it refuses a move from https to another scheme, any move to another
// host or port, and any change of method. net/http sends a POST, PUT or
// DELETE that is answered 301, 302 or 303 again as a GET with no body, so
// following such a redirect would turn the write into a read.
func sameOrigin(req *http.Request, via []*http.Request) error {
	first := via[0]
	from, to := first.URL, req.URL
	if strings.EqualFold(from.Scheme, "https") && !strings.EqualFold(to.Scheme, "https") {
		return fmt.Errorf("%w: from https to %s", ErrRedirect, to.Scheme)
	}
	if !strings.EqualFold(from.Hostname(), to.Hostname()) || portOf(from) != portOf(to) {
		return fmt.Errorf("%w: to another host or port (%s)", ErrRedirect, to.Host)
	}
	if req.Method != first.Method {
		return fmt.Errorf("%w: it would send the %s again as a %s", ErrRedirect, first.Method, req.Method)
	}
	return nil
}

// portOf returns u's port, or else the default port of its scheme.
func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}
