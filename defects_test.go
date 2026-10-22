// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

// The tests in this file use only the API of v1.1.0, and each one fails on
// v1.1.0: copied next to the v1.1.0 sources, the file compiles and every
// test in it fails, which shows that each test catches its defect.
// (TestD6_AnExpiresInOfCenturiesDoesNotWrapAround needs a 64-bit int, and
// skips elsewhere.) The tests that use only the v1.1.0 API but pass on
// v1.1.0 too, because they check that a fix keeps what v1.1.0 did, are in
// guards_test.go. Tests that need the API added in v1.2.0 are in
// api_test.go.
//
// Some tests replace http.DefaultTransport or http.DefaultClient.Transport,
// so no test in this package may call t.Parallel.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	gotoken "go/token"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testClientID = "client-123"
	testSecret   = "s3cret-value"
	testProperty = "1.0/library"
)

// hostPort returns the scheme, host and port of an httptest server's URL.
func hostPort(t *testing.T, rawURL string) (proto, host string, port int) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err = strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return u.Scheme, u.Hostname(), port
}

// newTestInstance returns an Instance of the DADI API that srv serves.
func newTestInstance(t *testing.T, srv *httptest.Server) *Instance {
	t.Helper()
	proto, host, port := hostPort(t, srv.URL)
	return New(proto, host, testProperty, testClientID, testSecret, port)
}

// fakeDADI starts a plain-http server that answers /token with token and
// every other path with api. The server is closed when the test ends.
func fakeDADI(t *testing.T, token, api http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(routeToken(token, api))
	t.Cleanup(srv.Close)
	return srv
}

// routeToken sends /token to token and every other path to api.
func routeToken(token, api http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			token(w, r)
			return
		}
		api(w, r)
	})
}

// issue answers a token request with tok, valid for expiresIn seconds.
func issue(tok string, expiresIn int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { writeToken(w, tok, expiresIn) }
}

func writeToken(w http.ResponseWriter, tok string, expiresIn int) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"accessToken":%q,"expiresIn":%d}`, tok, expiresIn)
}

// answer answers every request with the JSON body.
func answer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}
}

// mustNotPanic calls f and reports a panic as a test failure instead of
// letting it end the test binary. It returns whether f panicked.
func mustNotPanic(t *testing.T, what string, f func()) (panicked bool) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			panicked = true
			t.Errorf("%s panicked: %v", what, p)
		}
	}()
	f()
	return false
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// trustTestTLS makes http.DefaultTransport, which the library uses when no
// client is supplied, trust the certificate that every httptest TLS server
// presents, until the test ends.
func trustTestTLS(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = orig })
}

// D1: concurrent callers share one token request, at the first call and
// again at every renewal, and every caller uses the token it fetched.
func TestD1_ConcurrentCallsShareOneTokenRequest(t *testing.T) {
	var fetches atomic.Int32
	var mu sync.Mutex
	sent := map[string]int{} // Authorization header -> requests
	srv := fakeDADI(t,
		func(w http.ResponseWriter, _ *http.Request) {
			n := fetches.Add(1)
			time.Sleep(50 * time.Millisecond) // keep the request open while the other callers arrive
			writeToken(w, fmt.Sprintf("t%d", n), 1)
		},
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			sent[r.Header.Get("Authorization")]++
			mu.Unlock()
			io.WriteString(w, `{"results":[]}`)
		})
	inst := newTestInstance(t, srv)

	stampede := func(round int) {
		t.Helper()
		const callers = 16
		start := make(chan struct{})
		errs := make(chan error, callers)
		var wg sync.WaitGroup
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				var res map[string]any
				_, _, err := inst.Do(context.Background(), &Get{Collection: "books"}, &res)
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("round %d: Do: %v", round, err)
			}
		}
	}

	stampede(1)
	if got := fetches.Load(); got != 1 {
		t.Fatalf("16 concurrent first calls made %d token requests; want 1 shared by all", got)
	}
	// t1 lives 1 s, so it is renewed after 0.5 s (half its life).
	time.Sleep(600 * time.Millisecond)
	stampede(2)
	if got := fetches.Load(); got != 2 {
		t.Fatalf("16 concurrent calls after the token expired made %d token requests; want 1", got-1)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := map[string]int{"Bearer t1": 16, "Bearer t2": 16}; !reflect.DeepEqual(sent, want) {
		t.Errorf("Authorization headers sent: %v; want %v", sent, want)
	}
}

// D2: a URL that does not parse is an error from Initialize, not a panic.
func TestD2_InitializeReportsABadURLInsteadOfPanicking(t *testing.T) {
	t.Run("HTTPRequest", func(t *testing.T) {
		h := &HTTPRequest{Method: http.MethodGet, URL: "http://example.com/%zz"}
		var err error
		if mustNotPanic(t, "HTTPRequest.Initialize", func() { err = h.Initialize() }) {
			return
		}
		if err == nil {
			t.Error("Initialize accepted http://example.com/%zz")
		}
	})
	t.Run("Delete with a percent sign in the ID", func(t *testing.T) {
		mustNotPanic(t, `Delete{ID: "50%off"}.Initialize`, func() {
			d := &Delete{Collection: "offers", ID: "50%off"}
			_ = d.Initialize("http", "127.0.0.1", testProperty, "token", 1)
		})
	})
	t.Run("GetBearer with no protocol", func(t *testing.T) {
		var tok string
		if mustNotPanic(t, `New("", ...).GetBearer`, func() {
			tok = New("", "127.0.0.1", testProperty, testClientID, testSecret, 1).GetBearer(context.Background())
		}) {
			return
		}
		if tok != "" {
			t.Errorf("GetBearer = %q; want \"\"", tok)
		}
	})
}

// D3: the token request never follows a redirect: the credentials are not
// sent again and no token from another host is taken.
func TestD3_TokenRequestNeverFollowsARedirect(t *testing.T) {
	var collected atomic.Int32
	var collectedBody atomic.Value
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		collected.Add(1)
		b, _ := io.ReadAll(r.Body)
		collectedBody.Store(string(b))
		writeToken(w, "collector-token", 3600)
	}))
	defer collector.Close()
	_, _, collectorPort := hostPort(t, collector.URL)
	target := fmt.Sprintf("http://localhost:%d/collect", collectorPort)

	for _, code := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			collected.Store(0)
			var apiAuth atomic.Value
			api := httptest.NewTLSServer(routeToken(
				func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target, code) },
				func(w http.ResponseWriter, r *http.Request) {
					apiAuth.Store(r.Header.Get("Authorization"))
					io.WriteString(w, `{}`)
				}))
			defer api.Close()
			trustTestTLS(t, api)
			inst := newTestInstance(t, api)

			if tok := inst.GetBearer(context.Background()); tok != "" {
				t.Errorf("GetBearer = %q, a token from the redirect target; want \"\"", tok)
			}
			if _, _, err := inst.Do(context.Background(), &Get{Collection: "books"}, nil); err == nil {
				t.Error("Do succeeded with a token from the redirect target")
			}
			if n := collected.Load(); n > 0 {
				t.Errorf("the token request followed a %d from https to %s: %d request(s) arrived there, last body %q",
					code, target, n, collectedBody.Load())
			}
			if a := apiAuth.Load(); a != nil {
				t.Errorf("the API request went out with Authorization %q", a)
			}
		})
	}
}

// D3: an API request follows a redirect only to the same scheme, host and
// port, so the bearer token never goes in plain text or to another server.
func TestD3_APIRequestRefusesADowngradeOrAnotherHostOrPort(t *testing.T) {
	var mu sync.Mutex
	var leaked []string
	record := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			leaked = append(leaked, name+" got Authorization "+strconv.Quote(r.Header.Get("Authorization")))
			mu.Unlock()
			io.WriteString(w, `{"from":"`+name+`"}`)
		}
	}
	plain := httptest.NewServer(record("http server"))
	defer plain.Close()
	otherPort := httptest.NewTLSServer(record("https server on another port"))
	defer otherPort.Close()

	var target atomic.Value
	api := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/1.0/library/books":
			http.Redirect(w, r, target.Load().(string), http.StatusFound)
		case "/1.0/library/moved":
			io.WriteString(w, `{"from":"api","auth":`+strconv.Quote(r.Header.Get("Authorization"))+`}`)
		default:
			http.NotFound(w, r)
		}
	}
	tlsAPI := httptest.NewTLSServer(routeToken(issue("secret-token", 3600), api))
	defer tlsAPI.Close()
	plainAPI := httptest.NewServer(routeToken(issue("secret-token", 3600), api))
	defer plainAPI.Close()
	trustTestTLS(t, tlsAPI)
	_, _, plainPort := hostPort(t, plain.URL)

	for _, tc := range []struct {
		name   string
		api    *httptest.Server
		target string
		follow bool
	}{
		{"https to http on the same host name", tlsAPI, plain.URL + "/1.0/library/books", false},
		{"https to another port", tlsAPI, otherPort.URL + "/1.0/library/books", false},
		{"http to another host name", plainAPI, fmt.Sprintf("http://localhost:%d/1.0/library/books", plainPort), false},
		{"https to the same scheme, host and port", tlsAPI, tlsAPI.URL + "/1.0/library/moved", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mu.Lock()
			leaked = nil
			mu.Unlock()
			target.Store(tc.target)
			body, _, err := newTestInstance(t, tc.api).Do(context.Background(), &Get{Collection: "books"}, nil)
			mu.Lock()
			defer mu.Unlock()
			if tc.follow {
				want := `{"from":"api","auth":"Bearer secret-token"}`
				if err != nil || string(body) != want {
					t.Errorf("Do = %s, %v; want %s", body, err, want)
				}
				return
			}
			if err == nil {
				t.Errorf("Do followed the redirect to %s and returned %s", tc.target, body)
			}
			if len(leaked) > 0 {
				t.Errorf("the redirect target was sent the request: %v", leaked)
			}
		})
	}
}

// libraryFile is one parsed non-test source file of the package.
type libraryFile struct {
	name string
	src  []byte
	file *ast.File
	fset *gotoken.FileSet
}

// librarySources parses the package's non-test Go files.
func librarySources(t *testing.T) []libraryFile {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := gotoken.NewFileSet()
	var files []libraryFile
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, libraryFile{name: name, src: src, file: f, fset: fset})
	}
	if len(files) == 0 {
		t.Fatal("no library source files found")
	}
	return files
}

// slogLogging are the log/slog functions that write to the default logger.
var slogLogging = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true, "Log": true, "LogAttrs": true,
	"DebugContext": true, "InfoContext": true, "WarnContext": true, "ErrorContext": true,
	"Default": true, "SetDefault": true,
}

// D4: a library returns errors; it never prints or logs.
func TestD4_TheLibraryNeverPrintsOrLogs(t *testing.T) {
	for _, f := range librarySources(t) {
		ast.Inspect(f.file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ImportSpec:
				if p, _ := strconv.Unquote(n.Path.Value); p == "log" {
					t.Errorf("%s: imports log", f.fset.Position(n.Pos()))
				}
			case *ast.SelectorExpr:
				pkg, ok := n.X.(*ast.Ident)
				if !ok {
					return true
				}
				if pkg.Name == "fmt" && strings.HasPrefix(n.Sel.Name, "Print") ||
					pkg.Name == "os" && (n.Sel.Name == "Stdout" || n.Sel.Name == "Stderr") ||
					pkg.Name == "slog" && slogLogging[n.Sel.Name] {
					t.Errorf("%s: %s.%s", f.fset.Position(n.Pos()), pkg.Name, n.Sel.Name)
				}
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok && (id.Name == "print" || id.Name == "println") {
					t.Errorf("%s: %s", f.fset.Position(n.Pos()), id.Name)
				}
			}
			return true
		})
	}
}

// D5: when no token can be had, Do returns an error and sends nothing,
// instead of sending the request with "Authorization: Bearer ".
func TestD5_ATokenFailureStopsTheRequest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token http.HandlerFunc
	}{
		{"token endpoint answers 500", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "token store down", http.StatusInternalServerError)
		}},
		{"token endpoint answers 401", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
		}},
		{"200 without accessToken", answer(`{"expiresIn":3600}`)},
		{"200 with an empty accessToken", answer(`{"accessToken":"","expiresIn":3600}`)},
		{"200 with expiresIn 0", answer(`{"accessToken":"abc","expiresIn":0}`)},
		{"200 without expiresIn", answer(`{"accessToken":"abc"}`)},
		{"200 with a negative expiresIn", answer(`{"accessToken":"abc","expiresIn":-1}`)},
		{"200 with a body that is not JSON", answer(`<html>maintenance</html>`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			var auth atomic.Value
			srv := fakeDADI(t, tc.token, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				auth.Store(r.Header.Get("Authorization"))
				io.WriteString(w, `{"results":[]}`)
			})
			inst := newTestInstance(t, srv)
			if _, _, err := inst.Do(context.Background(), &Get{Collection: "books"}, nil); err == nil {
				t.Error("Do succeeded without a valid token")
			}
			if n := requests.Load(); n > 0 {
				t.Errorf("Do sent the request without a valid token (Authorization %q)", auth.Load())
			}
			if tok := inst.GetBearer(context.Background()); tok != "" {
				t.Errorf("GetBearer = %q; want \"\"", tok)
			}
		})
	}
}

// D6: a token that lives less than two minutes is renewed halfway through
// its life rather than at once, so it is used while it is valid.
func TestD6_AShortLivedTokenIsReused(t *testing.T) {
	t.Run("IsExpired", func(t *testing.T) {
		now := time.Now()
		for _, tc := range []struct {
			name string
			b    Bearer
			want bool
		}{
			{"30 s token with 16 s left (renewed 15 s before expiry)", Bearer{ExpiresIn: 30, ExpirationDate: now.Add(16 * time.Second)}, false},
			{"30 s token with 14 s left", Bearer{ExpiresIn: 30, ExpirationDate: now.Add(14 * time.Second)}, true},
			{"1 h token with 61 s left (renewed 60 s before expiry)", Bearer{ExpiresIn: 3600, ExpirationDate: now.Add(61 * time.Second)}, false},
			{"1 h token with 59 s left", Bearer{ExpiresIn: 3600, ExpirationDate: now.Add(59 * time.Second)}, true},
			{"unknown lifetime with 61 s left", Bearer{ExpirationDate: now.Add(61 * time.Second)}, false},
			{"unknown lifetime with 59 s left", Bearer{ExpirationDate: now.Add(59 * time.Second)}, true},
			{"expired", Bearer{ExpiresIn: 30, ExpirationDate: now.Add(-time.Second)}, true},
		} {
			if got := tc.b.IsExpired(); got != tc.want {
				t.Errorf("%s: IsExpired = %t; want %t", tc.name, got, tc.want)
			}
		}
		for _, expiresIn := range []int{1, 2, 30, 59, 60, 119, 120, 3600} {
			b, err := NewBearerFromJSON([]byte(fmt.Sprintf(`{"accessToken":"x","expiresIn":%d}`, expiresIn)))
			if err != nil {
				t.Fatal(err)
			}
			if b.IsExpired() {
				t.Errorf("a token that lives %d s counts as expired as soon as it arrives", expiresIn)
			}
		}
	})
	t.Run("one token request for repeated calls", func(t *testing.T) {
		var fetches atomic.Int32
		srv := fakeDADI(t, func(w http.ResponseWriter, _ *http.Request) {
			fetches.Add(1)
			writeToken(w, "t", 30)
		}, answer(`{}`))
		inst := newTestInstance(t, srv)
		for i := 0; i < 5; i++ {
			if tok := inst.GetBearer(context.Background()); tok != "t" {
				t.Fatalf("GetBearer = %q; want t", tok)
			}
		}
		if got := fetches.Load(); got != 1 {
			t.Errorf("5 GetBearer calls with a 30 s token made %d token requests; want 1", got)
		}
	})
}

// D7: the body of an error response is closed, so its connection is reused.
func TestD7_AnErrorResponseIsClosedAndItsConnectionReused(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(routeToken(issue("t", 3600), func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such collection", http.StatusNotFound)
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	inst := newTestInstance(t, srv)
	for i := 0; i < 5; i++ {
		if _, _, err := inst.Do(context.Background(), &Get{Collection: "missing"}, nil); err == nil {
			t.Fatal("Do returned no error for a 404")
		}
	}
	if got := conns.Load(); got != 1 {
		t.Errorf("a token request and five 404s opened %d connections; want 1, reused", got)
	}
}

// D8: a response body over the 64 MiB default is an error, not read into
// memory without limit.
func TestD8_AResponseBodyOverTheLimitIsAnError(t *testing.T) {
	const limit = 64 << 20 // the documented default
	chunk := bytes.Repeat([]byte{'a'}, 1<<20)
	for _, withLength := range []bool{false, true} {
		name := "chunked"
		if withLength {
			name = "with Content-Length"
		}
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if withLength {
					w.Header().Set("Content-Length", strconv.Itoa(limit+1))
				}
				for i := 0; i < limit/len(chunk); i++ {
					if _, err := w.Write(chunk); err != nil {
						return
					}
				}
				w.Write([]byte{'a'})
			}))
			defer srv.Close()
			proto, host, port := hostPort(t, srv.URL)
			g := &Get{Collection: "huge"}
			if err := g.Initialize(proto, host, testProperty, "token", port); err != nil {
				t.Fatal(err)
			}
			body, _, err := g.Do(context.Background())
			if err == nil {
				t.Errorf("read a %d-byte body (%d bytes returned) with no error; want an error over the 64 MiB default",
					limit+1, len(body))
			}
		})
	}
}

// D9: filter and sort input is encoded as JSON strings, so it cannot add
// conditions or break the query.
func TestD9_FilterAndSortInputCannotInjectJSON(t *testing.T) {
	const evil = `x"}, "password": {"$ne": "`
	type query = map[string]map[string]any
	for _, tc := range []struct {
		name string
		got  string
		want query
	}{
		{"string value", NewFilters(StrFilter("email", Equals, evil)).ToQueryString(), query{"email": {"$eq": evil}}},
		{"string filter field", NewFilters(StrFilter(evil, Equals, "a")).ToQueryString(), query{evil: {"$eq": "a"}}},
		{"string filter operator", NewFilters(StrFilter("email", evil, "a")).ToQueryString(), query{"email": {evil: "a"}}},
		{"one-of value", NewFilters(IsOneOf("role", []string{"user", `admin"], "$nin": ["`})).ToQueryString(),
			query{"role": {"$in": []any{"user", `admin"], "$nin": ["`}}}},
		{"one-of field", NewFilters(IsOneOf(evil, []string{"a"})).ToQueryString(), query{evil: {"$in": []any{"a"}}}},
		{"bool filter field", NewFilters(BoolFilter(evil, Equals, true)).ToQueryString(), query{evil: {"$eq": true}}},
		{"bool filter operator", NewFilters(BoolFilter("a", evil, true)).ToQueryString(), query{"a": {evil: true}}},
		{"number filter field", NewFilters(NumFilter(evil, GreaterThan, 7)).ToQueryString(), query{evil: {"$gt": float64(7)}}},
		{"field-exists field", NewFilters(FieldExists(evil, true)).ToQueryString(), query{evil: {"$ne": nil}}},
		{"regex with backslashes", NewFilters(StrFilter("isbn", Regex, `^\d{13}$`)).ToQueryString(), query{"isbn": {"$regex": `^\d{13}$`}}},
		{"control character", NewFilters(StrFilter("title", Equals, "a\nb\tc")).ToQueryString(), query{"title": {"$eq": "a\nb\tc"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got query
			if err := json.Unmarshal([]byte(tc.got), &got); err != nil {
				t.Fatalf("%s is not valid JSON: %v", tc.got, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s\nparses as %v; want %v", tc.got, got, tc.want)
			}
		})
	}
	t.Run("sort key", func(t *testing.T) {
		const key = `a": 1, "b`
		s := NewSort().Asc(key).String()
		var got map[string]int
		if err := json.Unmarshal([]byte(s), &got); err != nil {
			t.Fatalf("%s is not valid JSON: %v", s, err)
		}
		if want := map[string]int{key: 1}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s parses as %v; want %v", s, got, want)
		}
	})
}

// D10: URLs are built with escaping, so an ID is one path segment whatever
// it contains, and the property and the collection keep their slashes.
func TestD10_URLsEscapeEachPathSegment(t *testing.T) {
	type seen struct{ method, requestURI, path string }
	var mu sync.Mutex
	var last seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = seen{r.Method, r.RequestURI, r.URL.Path}
		mu.Unlock()
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	proto, host, port := hostPort(t, srv.URL)

	for _, tc := range []struct {
		name     string
		property string
		req      Request
		wantURI  string // the path as sent, without the query
		wantPath string // the path as the server decodes it
	}{
		{"collection", testProperty, &Post{Collection: "books"}, "/1.0/library/books", "/1.0/library/books"},
		{"count", testProperty, &Get{Collection: "books", Metadata: true}, "/1.0/library/books/count", "/1.0/library/books/count"},
		{"ID with a slash, a question mark and a hash", testProperty, &Put{Collection: "books", ID: "a/b?c#d"},
			"/1.0/library/books/a%2Fb%3Fc%23d", "/1.0/library/books/a/b?c#d"},
		{"ID with a percent sign", testProperty, &Delete{Collection: "offers", ID: "50%off"},
			"/1.0/library/offers/50%25off", "/1.0/library/offers/50%off"},
		{"ID with a space", testProperty, &Post{Collection: "books", ID: "x y"}, "/1.0/library/books/x%20y", "/1.0/library/books/x y"},
		{"ID that is ..", testProperty, &Delete{Collection: "books", ID: ".."}, "/1.0/library/books/%2E%2E", "/1.0/library/books/.."},
		{"collection keeps its slashes; a .. segment is escaped", testProperty, &Get{Collection: "books/../users"},
			"/1.0/library/books/%2E%2E/users", "/1.0/library/books/../users"},
		{"property segments are escaped, its slashes kept", "1.0/my library?x", &Get{Collection: "books"},
			"/1.0/my%20library%3Fx/books", "/1.0/my library?x/books"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if mustNotPanic(t, "Initialize", func() { err = tc.req.Initialize(proto, host, tc.property, "token", port) }) {
				return
			}
			if err != nil {
				t.Fatalf("Initialize: %v", err)
			}
			if _, _, err := tc.req.Do(context.Background()); err != nil {
				t.Fatalf("Do: %v", err)
			}
			mu.Lock()
			got := last
			mu.Unlock()
			uri, _, _ := strings.Cut(got.requestURI, "?")
			if uri != tc.wantURI || got.path != tc.wantPath {
				t.Errorf("sent %s %s (decoded path %q); want path %s (decoded %q)",
					got.method, got.requestURI, got.path, tc.wantURI, tc.wantPath)
			}
		})
	}
}

// D10: an IPv6 host is bracketed in the URL.
func TestD10_AnIPv6HostIsBracketed(t *testing.T) {
	for _, host := range []string{"::1", "[::1]"} {
		g := &Get{Collection: "books"}
		var err error
		if mustNotPanic(t, "Initialize", func() { err = g.Initialize("http", host, testProperty, "token", 8080) }) {
			continue
		}
		if err != nil {
			t.Errorf("Initialize with host %s: %v", host, err)
			continue
		}
		b, err := g.MarshalBinary()
		if want := "http://[::1]:8080/1.0/library/books"; err != nil || string(b) != want {
			t.Errorf("host %s: URL %s, %v; want %s", host, b, err, want)
		}
	}
	t.Run("request over IPv6 loopback", func(t *testing.T) {
		ln, err := net.Listen("tcp", "[::1]:0")
		if err != nil {
			t.Skipf("no IPv6 loopback here: %v", err)
		}
		srv := httptest.NewUnstartedServer(routeToken(issue("t6", 3600), answer(`{"ok":true}`)))
		srv.Listener.Close()
		srv.Listener = ln
		srv.Start()
		defer srv.Close()
		port := ln.Addr().(*net.TCPAddr).Port
		inst := New("http", "::1", testProperty, testClientID, testSecret, port)
		var res struct{ OK bool }
		if mustNotPanic(t, "Do", func() { _, _, err = inst.Do(context.Background(), &Get{Collection: "books"}, &res) }) {
			return
		}
		if err != nil || !res.OK {
			t.Errorf("Do over [::1]:%d = %+v, %v; want ok", port, res, err)
		}
	})
}

// D11: sort fields keep the order of the Asc and Desc calls; fields written
// straight into Value come out in sorted order; values print in decimal.
func TestD11_SortOrderIsDeterministic(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() *Sort
		want  string
	}{
		{"Asc(publishedAt).Desc(title)", func() *Sort { return NewSort().Asc("publishedAt").Desc("title") },
			`{"publishedAt": 1,"title": -1}`},
		{"Desc(title).Asc(publishedAt)", func() *Sort { return NewSort().Desc("title").Asc("publishedAt") },
			`{"title": -1,"publishedAt": 1}`},
		{"fields written straight into Value", func() *Sort { return &Sort{Value: map[string]int8{"c": 1, "a": 1, "b": -1}} },
			`{"a": 1,"b": -1,"c": 1}`},
	} {
		seen := map[string]int{}
		for i := 0; i < 200; i++ {
			seen[tc.build().String()]++
		}
		if len(seen) != 1 || seen[tc.want] != 200 {
			t.Errorf("200 builds of %s gave %v; want %s every time", tc.name, seen, tc.want)
		}
	}
	if got := NewSort().Asc("a").Desc("b").Desc("a").String(); got != `{"a": -1,"b": -1}` {
		t.Errorf("re-adding a field: %s; want {\"a\": -1,\"b\": -1} (first position, last direction)", got)
	}
	if got := (&Sort{Value: map[string]int8{"rank": 2}}).String(); got != `{"rank": 2}` {
		t.Errorf("a value of 2 prints as %s; want {\"rank\": 2}", got)
	}
}

// D12: no nil-pointer panics from a Get with no sort, a zero Sort, a request
// that was not initialized, an Instance built as a struct literal, or a token
// endpoint that answers null.
func TestD12_NoNilPointerPanics(t *testing.T) {
	ctx := context.Background()
	t.Run("printing a Get with no sort", func(t *testing.T) {
		for _, v := range []any{Get{Collection: "books"}, &Get{Collection: "books"}} {
			if s := fmt.Sprint(v); strings.Contains(s, "PANIC") {
				t.Errorf("fmt.Sprint(%T) = %s", v, s)
			}
		}
	})
	t.Run("Asc and Desc on a Sort with a nil map", func(t *testing.T) {
		var s Sort
		if mustNotPanic(t, "Sort{}.Asc", func() { s.Asc("title").Desc("year") }) {
			return
		}
		if got := s.String(); got != `{"title": 1,"year": -1}` {
			t.Errorf("String = %s; want {\"title\": 1,\"year\": -1}", got)
		}
	})
	t.Run("MarshalBinary and Do before Initialize", func(t *testing.T) {
		for _, r := range []Request{&Get{}, &Post{}, &Put{}, &Delete{}} {
			var err error
			if !mustNotPanic(t, fmt.Sprintf("%T.MarshalBinary", r), func() { _, err = r.MarshalBinary() }) && err == nil {
				t.Errorf("%T.MarshalBinary before Initialize returned no error", r)
			}
			if !mustNotPanic(t, fmt.Sprintf("%T.Do", r), func() { _, _, err = r.Do(ctx) }) && err == nil {
				t.Errorf("%T.Do before Initialize returned no error", r)
			}
		}
	})
	t.Run("Instance built as a struct literal", func(t *testing.T) {
		inst := &Instance{Proto: "http", Hostname: "127.0.0.1", Port: 1, Property: testProperty}
		var tok string
		if !mustNotPanic(t, "GetBearer", func() { tok = inst.GetBearer(ctx) }) && tok != "" {
			t.Errorf("GetBearer = %q; want \"\"", tok)
		}
		var err error
		if !mustNotPanic(t, "Do", func() { _, _, err = inst.Do(ctx, &Get{Collection: "books"}, nil) }) && err == nil {
			t.Error("Do on an Instance with no credentials returned no error")
		}
	})
	t.Run("token endpoint answers null", func(t *testing.T) {
		var requests atomic.Int32
		srv := fakeDADI(t, answer(`null`), func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			io.WriteString(w, `{}`)
		})
		inst := newTestInstance(t, srv)
		var tok string
		if !mustNotPanic(t, "GetBearer", func() { tok = inst.GetBearer(ctx) }) && tok != "" {
			t.Errorf("GetBearer = %q; want \"\"", tok)
		}
		var err error
		if !mustNotPanic(t, "Do", func() { _, _, err = inst.Do(ctx, &Get{Collection: "books"}, nil) }) && err == nil {
			t.Error("Do succeeded with a null token response")
		}
		if n := requests.Load(); n > 0 {
			t.Errorf("Do sent %d request(s) with no token", n)
		}
	})
	t.Run("NewBearerFromJSON(null)", func(t *testing.T) {
		mustNotPanic(t, "NewBearerFromJSON(null)", func() { _, _ = NewBearerFromJSON([]byte("null")) })
	})
}

// D13: Do decodes into res when res is a pointer, reports a res it cannot
// decode into, and skips decoding when res is nil.
func TestD13_DoDecodesIntoResOrSaysWhyNot(t *testing.T) {
	srv := fakeDADI(t, issue("t", 3600), func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/plain") {
			io.WriteString(w, "OK")
			return
		}
		io.WriteString(w, `{"results":[{"title":"Dune"}]}`)
	})
	inst := newTestInstance(t, srv)
	ctx := context.Background()
	type page struct{ Results []struct{ Title string } }

	t.Run("pointer", func(t *testing.T) {
		var res page
		if _, _, err := inst.Do(ctx, &Get{Collection: "books"}, &res); err != nil || len(res.Results) != 1 || res.Results[0].Title != "Dune" {
			t.Errorf("Do = %+v, %v; want one result, Dune", res, err)
		}
	})
	t.Run("non-pointer", func(t *testing.T) {
		var res page
		if _, _, err := inst.Do(ctx, &Get{Collection: "books"}, res); err == nil {
			t.Error("Do took a non-pointer res, decoded into a copy and threw it away, with no error")
		}
	})
	t.Run("nil pointer", func(t *testing.T) {
		var res *page
		if _, _, err := inst.Do(ctx, &Get{Collection: "books"}, res); err == nil {
			t.Error("Do took a nil *page, decoded into a copy and threw it away, with no error")
		}
	})
	t.Run("nil res skips decoding", func(t *testing.T) {
		body, _, err := inst.Do(ctx, &Get{Collection: "plain"}, nil)
		if err != nil || string(body) != "OK" {
			t.Errorf("Do = %q, %v; want \"OK\", no error (nothing to decode into)", body, err)
		}
	})
}

// D14: a Delete with no ID is refused, rather than sent to the collection.
func TestD14_ADeleteWithNoIDIsRefused(t *testing.T) {
	d := &Delete{Collection: "books"}
	if err := d.Initialize("http", "127.0.0.1", testProperty, "token", 1); err == nil {
		u, _ := d.MarshalBinary()
		t.Errorf("Initialize accepted a Delete with no ID; it would send DELETE %s", u)
	}
	var deletes atomic.Int32
	srv := fakeDADI(t, issue("t", 3600), func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes.Add(1)
		}
		io.WriteString(w, `{}`)
	})
	if _, _, err := newTestInstance(t, srv).Do(context.Background(), &Delete{Collection: "books"}, nil); err == nil {
		t.Error("Do returned no error for a Delete with no ID")
	}
	if n := deletes.Load(); n > 0 {
		t.Errorf("%d DELETE request(s) reached the server", n)
	}
}

// D15: If-None-Match is sent only when there is an ETag.
func TestD15_IfNoneMatchIsSentOnlyWithAnETag(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := r.Header["If-None-Match"]
		mu.Lock()
		if ok {
			got = append(got, strconv.Quote(strings.Join(v, ",")))
		} else {
			got = append(got, "absent")
		}
		mu.Unlock()
		if ok && v[0] == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	proto, host, port := hostPort(t, srv.URL)
	for _, g := range []*Get{{Collection: "books"}, {Collection: "books", ETag: `"v1"`}} {
		if err := g.Initialize(proto, host, testProperty, "token", port); err != nil {
			t.Fatal(err)
		}
		body, headers, err := g.Do(context.Background())
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		if g.ETag != "" && (body != nil || headers == nil) {
			t.Errorf("304: body %q, headers %v; want no body and the headers", body, headers)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"absent", `"\"v1\""`}; !reflect.DeepEqual(got, want) {
		t.Errorf("If-None-Match sent: %v; want %v", got, want)
	}
}

// D16: the library does not send through http.DefaultClient, which any
// other package can reconfigure.
func TestD16_HTTPDefaultClientIsNotUsed(t *testing.T) {
	srv := fakeDADI(t, issue("t", 3600), answer(`{}`))
	orig := http.DefaultClient.Transport
	var used atomic.Int32
	http.DefaultClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used.Add(1)
		if r.Body != nil {
			r.Body.Close()
		}
		return nil, errors.New("sent through http.DefaultClient")
	})
	t.Cleanup(func() { http.DefaultClient.Transport = orig })

	if _, _, err := newTestInstance(t, srv).Do(context.Background(), &Get{Collection: "books"}, nil); err != nil {
		t.Errorf("Do: %v", err)
	}
	if n := used.Load(); n > 0 {
		t.Errorf("%d request(s) went through http.DefaultClient, which another package had changed", n)
	}
}

// printVerbs are the fmt verbs, with some flags, that the redaction tests
// try. %T and %p never call a method, and print only the type or an address.
var printVerbs = []string{
	"%v", "%+v", "%#v", "%s", "%q", "%#q", "%x", "%X", "% x", "%#x", "%10s", "%-30v",
	"%d", "%t", "%o", "%O", "%b", "%e", "%E", "%f", "%F", "%g", "%G", "%c", "%U", "%#U",
}

// shows returns the form in which s shows one of the secrets, as it is or
// in hex, or "" when it shows none.
func shows(s string, secrets ...string) string {
	for _, v := range secrets {
		for _, form := range []string{v, hex.EncodeToString([]byte(v)), strings.ToUpper(hex.EncodeToString([]byte(v)))} {
			if strings.Contains(s, form) {
				return form
			}
		}
	}
	return ""
}

// D18: printing or logging Credentials or a Bearer never shows the secret or
// the token, whatever the verb; it shows the client id and the expiry. JSON
// is unchanged.
func TestD18_CredentialsAndBearerAreRedacted(t *testing.T) {
	const secret, tok = testSecret, "tok-value-123"
	c := Credentials{ClientID: testClientID, Secret: secret}
	b := Bearer{ExpiresIn: 3600, ExpirationDate: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), Value: tok}
	leak := func(s string) string { return shows(s, secret, tok) }

	for _, verb := range printVerbs {
		for _, v := range []any{c, &c, b, &b, []Credentials{c}, map[string]Bearer{"b": b},
			struct {
				C Credentials
				B Bearer
			}{c, b}} {
			if out := fmt.Sprintf(verb, v); leak(out) != "" {
				t.Errorf("fmt.Sprintf(%q, %T) shows %q: %s", verb, v, leak(out), out)
			}
		}
	}
	if s := fmt.Sprint(c); !strings.Contains(s, testClientID) {
		t.Errorf("fmt.Sprint(Credentials) = %s; want it to show the client id", s)
	}
	if s := fmt.Sprint(b); !strings.Contains(s, "2026-10-22") {
		t.Errorf("fmt.Sprint(Bearer) = %s; want it to show the expiry", s)
	}

	for name, newHandler := range map[string]func(io.Writer) slog.Handler{
		"JSON": func(w io.Writer) slog.Handler { return slog.NewJSONHandler(w, nil) },
		"text": func(w io.Writer) slog.Handler { return slog.NewTextHandler(w, nil) },
	} {
		var buf bytes.Buffer
		slog.New(newHandler(&buf)).Info("auth", "credentials", c, "credentialsPtr", &c, "bearer", b, "bearerPtr", &b)
		if l := leak(buf.String()); l != "" {
			t.Errorf("slog %s handler shows %q: %s", name, l, buf.String())
		}
		if !strings.Contains(buf.String(), testClientID) {
			t.Errorf("slog %s handler: %s; want it to show the client id", name, buf.String())
		}
	}

	wantJSON := `{"clientId":"` + testClientID + `","secret":"` + secret + `"}`
	if j, err := json.Marshal(c); err != nil || string(j) != wantJSON {
		t.Errorf("json.Marshal(Credentials) = %s, %v; want %s", j, err, wantJSON)
	}
	if j, err := c.ToJSON(); err != nil || string(j) != wantJSON {
		t.Errorf("Credentials.ToJSON = %s, %v; want %s", j, err, wantJSON)
	}
	nb, err := NewBearerFromJSON([]byte(`{"accessToken":"tok","expiresIn":60}`))
	if err != nil || nb.Value != "tok" || nb.ExpiresIn != 60 {
		t.Errorf("NewBearerFromJSON = %#v, %v; want tok, 60", nb, err)
	}
	var db Bearer
	if err := json.Unmarshal([]byte(`{"accessToken":"tok","expiresIn":60}`), &db); err != nil || db.Value != "tok" || db.ExpiresIn != 60 {
		t.Errorf("json.Unmarshal(Bearer) = %#v, %v; want tok, 60", db, err)
	}
}

// D18: no fmt verb shows the client secret or the bearer token held by a
// Wallet, or the token in the Authorization header of a request that was
// sent. An Instance, and the requests themselves, print no secret either.
// (Fails on v1.1.0 too: %s on a *Wallet or an HTTPRequest showed them.)
func TestD18_AWalletAndARequestNeverPrintTheSecretOrTheToken(t *testing.T) {
	const tok = "tok-PROBE-9f8e7d"
	srv := fakeDADI(t, issue(tok, 3600), answer(`{}`))
	ctx := context.Background()
	proto, host, port := hostPort(t, srv.URL)
	w := NewWallet(proto, host, testClientID, testSecret, port)
	if got := w.GetBearer(ctx); got != tok {
		t.Fatalf("Wallet.GetBearer = %q; want %s", got, tok)
	}
	inst := newTestInstance(t, srv)
	get := &Get{Collection: "books"}
	if _, _, err := inst.Do(ctx, get, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := get.HTTPRequest.Req.Header.Get("Authorization"); got != "Bearer "+tok {
		t.Fatalf("Authorization %q; want Bearer %s", got, tok)
	}
	for _, verb := range printVerbs {
		for _, v := range []any{w, *w, get.HTTPRequest, *get.HTTPRequest, inst, *inst, get, *get} {
			for _, out := range []string{fmt.Sprintf(verb, v), fmt.Sprint(v), fmt.Sprintln(v)} {
				if l := shows(out, testSecret, tok); l != "" {
					t.Errorf("fmt.Sprintf(%q, %T) shows %q: %s", verb, v, l, out)
				}
			}
		}
	}
	if s := fmt.Sprint(w); !strings.Contains(s, testClientID) || !strings.Contains(s, "/token") {
		t.Errorf("fmt.Sprint(*Wallet) = %s; want it to show the client id and the token URL", s)
	}
	if s := fmt.Sprint(get.HTTPRequest); !strings.Contains(s, http.MethodGet) || !strings.Contains(s, "/1.0/library/books") {
		t.Errorf("fmt.Sprint(*HTTPRequest) = %s; want it to show the method and the URL", s)
	}
}

// D19: the sources are gofmt-ed and use any and io, not interface{} and
// io/ioutil.
func TestD19_SourcesAreFormattedAndCurrent(t *testing.T) {
	for _, f := range librarySources(t) {
		if formatted, err := format.Source(f.src); err != nil {
			t.Errorf("%s: %v", f.name, err)
		} else if !bytes.Equal(formatted, f.src) {
			t.Errorf("%s is not gofmt-ed", f.name)
		}
		ast.Inspect(f.file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ImportSpec:
				if p, _ := strconv.Unquote(n.Path.Value); p == "io/ioutil" {
					t.Errorf("%s: imports io/ioutil; use io", f.fset.Position(n.Pos()))
				}
			case *ast.InterfaceType:
				if n.Methods == nil || len(n.Methods.List) == 0 {
					t.Errorf("%s: interface{}; use any", f.fset.Position(n.Pos()))
				}
			}
			return true
		})
	}
}

// The tests below were added for the review of v1.2.0.

// D6: an expiresIn of centuries is a token valid for centuries. It used to
// overflow time.Duration and land in the past, so every call fetched a new
// token. A hugely negative one is expired. (Fails on v1.1.0 too.)
func TestD6_AnExpiresInOfCenturiesDoesNotWrapAround(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("int has 32 bits here, so expiresIn cannot exceed 68 years")
	}
	for _, expiresIn := range []int64{9223372036, 9223372037, 10000000000, math.MaxInt64} {
		b, err := NewBearerFromJSON([]byte(fmt.Sprintf(`{"accessToken":"x","expiresIn":%d}`, expiresIn)))
		if err != nil {
			t.Fatal(err)
		}
		if b.IsExpired() || b.ExpirationDate.Before(time.Now().AddDate(250, 0, 0)) {
			t.Errorf("expiresIn %d: ExpirationDate %v, IsExpired %t; want a date centuries away", expiresIn, b.ExpirationDate, b.IsExpired())
		}
	}
	for _, expiresIn := range []int64{-9223372037, math.MinInt64} {
		b, err := NewBearerFromJSON([]byte(fmt.Sprintf(`{"accessToken":"x","expiresIn":%d}`, expiresIn)))
		if err != nil {
			t.Fatal(err)
		}
		if !b.IsExpired() {
			t.Errorf("expiresIn %d: ExpirationDate %v, not expired; want expired", expiresIn, b.ExpirationDate)
		}
	}

	var fetches atomic.Int32
	srv := fakeDADI(t, func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		io.WriteString(w, `{"accessToken":"t","expiresIn":10000000000}`)
	}, answer(`{}`))
	inst := newTestInstance(t, srv)
	for i := 0; i < 5; i++ {
		if tok := inst.GetBearer(context.Background()); tok != "t" {
			t.Fatalf("GetBearer = %q; want t", tok)
		}
	}
	if n := fetches.Load(); n != 1 {
		t.Errorf("5 GetBearer calls with a token valid for 317 years made %d token requests; want 1", n)
	}
}

// D3: a redirect never turns a write into a read. net/http sends a POST, PUT
// or DELETE that is answered 301, 302 or 303 again as a GET with no body;
// Do must not report that GET's answer as the write's. A 307 or 308 keeps
// the method and the body, and is followed. (Fails on v1.1.0 too.)
func TestD3_ARedirectNeverTurnsAWriteIntoARead(t *testing.T) {
	var mu sync.Mutex
	var landed []string
	srv := fakeDADI(t, issue("t", 3600), func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/landing" {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			landed = append(landed, r.Method+" "+string(b))
			mu.Unlock()
			io.WriteString(w, `{"landed":true}`)
			return
		}
		// /1.0/library/<code>[/<id>] answers <code> with a redirect to /landing.
		code, err := strconv.Atoi(strings.Split(strings.TrimPrefix(r.URL.Path, "/1.0/library/"), "/")[0])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/landing", code)
	})
	inst := newTestInstance(t, srv)
	take := func() []string {
		mu.Lock()
		defer mu.Unlock()
		l := landed
		landed = nil
		return l
	}
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther} {
		c := strconv.Itoa(code)
		for _, req := range []Request{
			&Post{Collection: c, Body: []byte(`{"title":"x"}`)},
			&Put{Collection: c, ID: "1", Body: []byte(`{"title":"y"}`)},
			&Delete{Collection: c, ID: "1"},
		} {
			body, _, err := inst.Do(context.Background(), req, nil)
			if err == nil {
				t.Errorf("%T answered %d: Do = %s with no error; want an error, not the answer to a GET", req, code, body)
			}
			if l := take(); len(l) > 0 {
				t.Errorf("%T answered %d: the redirect was followed as %q", req, code, l)
			}
		}
	}
	for _, code := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		body, _, err := inst.Do(context.Background(), &Put{Collection: strconv.Itoa(code), ID: "1", Body: []byte(`{"title":"y"}`)}, nil)
		if l := take(); err != nil || string(body) != `{"landed":true}` || !reflect.DeepEqual(l, []string{`PUT {"title":"y"}`}) {
			t.Errorf("*Put answered %d: Do = %s, %v, and the target saw %q; want the PUT followed with its body", code, body, err, l)
		}
	}
}

// Do: a 204 No Content, which DADI API answers to a DELETE unless its
// feedback setting is on, is a success with no body. (Fails on v1.1.0 too.)
func TestDo_A204NoContentIsASuccess(t *testing.T) {
	var deletes atomic.Int32
	srv := fakeDADI(t, issue("t", 3600), func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes.Add(1)
		}
		w.Header().Set("X-Request", r.Method)
		w.WriteHeader(http.StatusNoContent)
	})
	inst := newTestInstance(t, srv)
	ctx := context.Background()
	body, headers, err := inst.Do(ctx, &Delete{Collection: "books", ID: "1"}, nil)
	if err != nil || body != nil || headers == nil || headers.Get("X-Request") != http.MethodDelete {
		t.Errorf("a DELETE answered 204: Do = %q, %v, %v; want no body, the headers and no error", body, headers, err)
	}
	if n := deletes.Load(); n != 1 {
		t.Errorf("%d DELETE requests; want 1", n)
	}
	var res map[string]any
	if body, _, err := inst.Do(ctx, &Get{Collection: "books"}, &res); err != nil || body != nil || res != nil {
		t.Errorf("a GET answered 204: Do = %q, %v, res %v; want no body, no error and res untouched", body, err, res)
	}
}

// D10: an IPv6 host with a zone works in each form: fe80::1%lo0,
// [fe80::1%lo0], and the URL form [fe80::1%25lo0], whose %25 is the zone
// separator and is decoded once. (v1.1.0 handled only the URL form, and
// panicked on the other two.)
func TestD10_AnIPv6ZoneIsSentOnce(t *testing.T) {
	for _, host := range []string{"fe80::1%lo0", "[fe80::1%lo0]", "[fe80::1%25lo0]"} {
		g := &Get{Collection: "books"}
		var err error
		if mustNotPanic(t, "Initialize with host "+host, func() { err = g.Initialize("http", host, testProperty, "token", 8080) }) {
			continue
		}
		if err != nil {
			t.Errorf("Initialize with host %s: %v", host, err)
			continue
		}
		b, err := g.MarshalBinary()
		if want := "http://[fe80::1%25lo0]:8080/1.0/library/books"; err != nil || string(b) != want {
			t.Errorf("host %s: URL %s, %v; want %s", host, b, err, want)
		}
	}
	t.Run("request over a link-local address", func(t *testing.T) {
		ln, ip, zone := listenLinkLocal(t)
		srv := httptest.NewUnstartedServer(routeToken(issue("t6", 3600), answer(`{"ok":true}`)))
		srv.Listener.Close()
		srv.Listener = ln
		srv.Start()
		defer srv.Close()
		port := ln.Addr().(*net.TCPAddr).Port
		inst := New("http", "["+ip+"%25"+zone+"]", testProperty, testClientID, testSecret, port)
		var res struct{ OK bool }
		var err error
		if mustNotPanic(t, "Do", func() { _, _, err = inst.Do(context.Background(), &Get{Collection: "books"}, &res) }) {
			return
		}
		if err != nil || !res.OK {
			t.Errorf("Do over [%s%%25%s]:%d = %+v, %v; want ok", ip, zone, port, res, err)
		}
	})
}

// listenLinkLocal listens on the first IPv6 link-local address it can, and
// returns the listener, the address and its zone. It skips the test when
// there is none.
func listenLinkLocal(t *testing.T) (net.Listener, string, string) {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("no interfaces: %v", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() != nil || !n.IP.IsLinkLocalUnicast() {
				continue
			}
			ln, err := net.Listen("tcp", net.JoinHostPort(n.IP.String()+"%"+iface.Name, "0"))
			if err == nil {
				return ln, n.IP.String(), iface.Name
			}
		}
	}
	t.Skip("no IPv6 link-local address to listen on here")
	return nil, "", ""
}
