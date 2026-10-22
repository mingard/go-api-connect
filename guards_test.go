// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

// Regression guards. Like the tests in defects_test.go, these use only the
// API of v1.1.0, but they pass on v1.1.0 too, by design: each one checks
// that a v1.2.0 fix keeps behaviour that v1.1.0 already had, so none of them
// catches a defect of v1.1.0; each test in defects_test.go does. They use
// the helpers in defects_test.go.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// D1: NewBearer still fetches a new token while the current one is valid,
// as in v1.1.0, and starts nothing once its context has ended. (This passes
// on v1.1.0 too: it guards the shared token request.)
func TestD1_NewBearerStillFetchesANewToken(t *testing.T) {
	var fetches atomic.Int32
	srv := fakeDADI(t, func(w http.ResponseWriter, _ *http.Request) {
		writeToken(w, fmt.Sprintf("t%d", fetches.Add(1)), 3600)
	}, answer(`{}`))
	proto, host, port := hostPort(t, srv.URL)
	w := NewWallet(proto, host, testClientID, testSecret, port)
	ctx := context.Background()
	if tok := w.GetBearer(ctx); tok != "t1" {
		t.Fatalf("GetBearer = %q; want t1", tok)
	}
	w.NewBearer(ctx)
	if tok := w.GetBearer(ctx); tok != "t2" {
		t.Errorf("after NewBearer, GetBearer = %q; want t2", tok)
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	w.NewBearer(done)
	if n := fetches.Load(); n != 2 {
		t.Errorf("%d token requests; want 2", n)
	}
}

// D1: a transport that calls runtime.Goexit (as t.Fatal does) or panics
// while it sends the shared token request does not wedge the Wallet: the
// caller returns, a panic reaches the caller's goroutine, and the next call
// sends a new token request. (v1.1.0 sent the request on the caller's
// goroutine, so it passes there too: this guards the shared request.)
func TestD1_ATransportThatExitsOrPanicsDoesNotWedgeTheWallet(t *testing.T) {
	const bug = "bug in a RoundTripper"
	for _, failure := range []struct {
		name string
		fail func()
		want any // what the caller recovers
	}{
		{"runtime.Goexit", runtime.Goexit, nil},
		{"panic", func() { panic(bug) }, bug},
	} {
		for _, call := range []struct {
			name string
			call func(inst *Instance, w *Wallet)
		}{
			{"Instance.GetBearer", func(inst *Instance, _ *Wallet) { inst.GetBearer(context.Background()) }},
			{"Wallet.NewBearer", func(_ *Instance, w *Wallet) { w.NewBearer(context.Background()) }},
		} {
			t.Run(failure.name+" in "+call.name, func(t *testing.T) {
				var fetches atomic.Int32
				srv := fakeDADI(t, func(w http.ResponseWriter, _ *http.Request) {
					writeToken(w, fmt.Sprintf("t%d", fetches.Add(1)), 3600)
				}, answer(`{}`))
				inst := newTestInstance(t, srv)
				proto, host, port := hostPort(t, srv.URL)
				wallet := NewWallet(proto, host, testClientID, testSecret, port)

				orig := http.DefaultTransport
				var calls atomic.Int32
				http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if calls.Add(1) == 1 {
						failure.fail()
					}
					return orig.RoundTrip(r)
				})
				t.Cleanup(func() { http.DefaultTransport = orig })

				recovered := make(chan any, 1)
				go func() {
					var p any
					defer func() { recovered <- p }() // runs on runtime.Goexit too
					defer func() { p = recover() }()
					call.call(inst, wallet)
				}()
				select {
				case p := <-recovered:
					if !reflect.DeepEqual(p, failure.want) {
						t.Errorf("the caller recovered %v; want %v", p, failure.want)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("the caller was still blocked 5 s after the transport's %s", failure.name)
				}

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for name, get := range map[string]func() string{
					"Instance.GetBearer": func() string { return inst.GetBearer(ctx) },
					"Wallet.GetBearer":   func() string { return wallet.GetBearer(ctx) },
				} {
					if tok := get(); !strings.HasPrefix(tok, "t") {
						t.Errorf("%s after the failure = %q; want a token from a new token request", name, tok)
					}
				}
			})
		}
	}
}

// D1: a nil context is a caller's bug and may panic, as in v1.1.0, but once
// that panic is recovered the Instance keeps working: the panic does not
// leave the Wallet locked. (Passes on v1.1.0.)
func TestD1_ANilContextDoesNotLeaveTheWalletLocked(t *testing.T) {
	var fetches atomic.Int32
	srv := fakeDADI(t, func(w http.ResponseWriter, _ *http.Request) {
		writeToken(w, fmt.Sprintf("t%d", fetches.Add(1)), 3600)
	}, answer(`{}`))
	inst := newTestInstance(t, srv)
	func() {
		defer func() { _ = recover() }()
		var nilCtx context.Context
		inst.GetBearer(nilCtx)
	}()
	done := make(chan string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done <- inst.GetBearer(ctx)
	}()
	select {
	case tok := <-done:
		if tok != "t1" {
			t.Errorf("GetBearer after a nil context = %q; want t1", tok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GetBearer after a nil context was still blocked 5 s later, with a 1 s context")
	}
}

// D1: a caller whose context has already ended, or ends while it waits for
// a renewal, still gets the current token while that token has not expired.
// (Passes on v1.1.0.)
func TestD1_AnEndedContextStillGetsATokenThatHasNotExpired(t *testing.T) {
	var fetches atomic.Int32
	release := make(chan struct{})
	var releaseOnce sync.Once
	srv := fakeDADI(t, func(w http.ResponseWriter, r *http.Request) {
		if fetches.Add(1) == 1 {
			writeToken(w, "t1", 2) // renewed 1 s before it expires
			return
		}
		select { // the renewal hangs
		case <-release:
		case <-r.Context().Done():
		}
		http.Error(w, "down", http.StatusServiceUnavailable)
	}, answer(`{}`))
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) }) // before the server closes
	inst := newTestInstance(t, srv)
	if tok := inst.GetBearer(context.Background()); tok != "t1" {
		t.Fatalf("GetBearer = %q; want t1", tok)
	}
	time.Sleep(1100 * time.Millisecond) // t1 is due for renewal, and expires in 0.9 s

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	if tok := inst.GetBearer(ended); tok != "t1" {
		t.Errorf("GetBearer with a context that has ended = %q; want t1, which has not expired", tok)
	}
	short, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if tok := inst.GetBearer(short); tok != "t1" {
		t.Errorf("GetBearer whose context ended while the renewal hung = %q; want t1, which has not expired", tok)
	}
}

// D1: a copy of a Wallet is the same wallet. A copy taken while a token
// request is in progress is not left with that request's outcome for good,
// and a Wallet holds no lock by value, so copying one is vet-clean. (Passes
// on v1.1.0, whose Wallet held no lock.)
func TestD1_ACopyOfAWalletKeepsWorking(t *testing.T) {
	var fetches atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	srv := fakeDADI(t, func(w http.ResponseWriter, _ *http.Request) {
		if fetches.Add(1) == 1 {
			close(started)
			<-release
			http.Error(w, "warming up", http.StatusServiceUnavailable)
			return
		}
		writeToken(w, "t2", 3600)
	}, answer(`{}`))
	t.Cleanup(unblock) // before the server closes
	proto, host, port := hostPort(t, srv.URL)
	w := NewWallet(proto, host, testClientID, testSecret, port)

	first := make(chan string, 1)
	go func() { first <- w.GetBearer(context.Background()) }()
	<-started
	cp := *w // a copy, taken while the token request is in progress
	unblock()
	if tok := <-first; tok != "" {
		t.Errorf("GetBearer whose token request failed = %q; want \"\"", tok)
	}
	if tok := cp.GetBearer(context.Background()); tok != "t2" {
		t.Errorf("the copy's GetBearer = %q; want t2, from a new token request", tok)
	}
	if tok := w.GetBearer(context.Background()); tok != "t2" {
		t.Errorf("the original's GetBearer = %q; want t2", tok)
	}

	locker := reflect.TypeOf((*sync.Locker)(nil)).Elem()
	var check func(typ reflect.Type, path string)
	check = func(typ reflect.Type, path string) {
		if reflect.PointerTo(typ).Implements(locker) {
			t.Errorf("%s (%s) is a lock held by value: copying a Wallet copies it", path, typ)
			return
		}
		switch typ.Kind() {
		case reflect.Struct:
			for i := 0; i < typ.NumField(); i++ {
				check(typ.Field(i).Type, path+"."+typ.Field(i).Name)
			}
		case reflect.Array:
			check(typ.Elem(), path+"[i]")
		}
	}
	check(reflect.TypeOf(Wallet{}), "Wallet")
}

// D7: closing an error response does not wait for a body that stalls, so Do
// returns promptly. (v1.1.0 never closed the body, so it passes there too.)
func TestD7_AnErrorBodyThatStallsDoesNotHoldDo(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	srv := fakeDADI(t, issue("t", 3600), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "0123456789") // 10 of the 100 bytes, then nothing
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) }) // before the server closes
	inst := newTestInstance(t, srv)
	if tok := inst.GetBearer(context.Background()); tok != "t" {
		t.Fatalf("GetBearer = %q; want t", tok)
	}
	begin := time.Now()
	_, _, err := inst.Do(context.Background(), &Get{Collection: "missing"}, nil)
	if elapsed := time.Since(begin); err == nil || !strings.Contains(err.Error(), "404") || elapsed > time.Second {
		t.Errorf("a 404 whose body stalls: Do returned %v after %v; want the 404 within a second", err, elapsed.Round(time.Millisecond))
	}
}

// D9: benign input gives exactly the v1.1.0 bytes, so nothing changes on the
// wire for existing callers. (This passes on v1.1.0 too: it guards the fix.)
func TestD9_BenignInputKeepsTheV110Format(t *testing.T) {
	got := NewFilters(
		StrFilter("title", Regex, "^The <b>Dune</b> & more"),
		NumFilter("year", GreaterThanOrEqualTo, 1965),
		BoolFilter("published", Equals, true),
		FieldExists("deletedAt", false),
		FieldExists("isbn", true),
		IsOneOf("format", []string{"hardback", "", "paperback"}),
		IsOneOf("tags", nil),
	).ToQueryString()
	want := `{"title": {"$regex": "^The <b>Dune</b> & more"},"year": {"$gte": 1965},"published": {"$eq": true},` +
		`"deletedAt": {"$eq": null},"isbn": {"$ne": null},"format": {"$in": ["hardback","paperback"]},"tags": {"$in": [""]}}`
	if got != want {
		t.Errorf("ToQueryString =\n%s\nwant\n%s", got, want)
	}
	if got := NewSort().Desc("publishedAt").String(); got != `{"publishedAt": -1}` {
		t.Errorf("Sort.String = %s; want {\"publishedAt\": -1}", got)
	}
	if got := (&Filters{}).ToQueryString(); got != "" {
		t.Errorf("empty Filters = %q; want \"\"", got)
	}
	if got := NewFilters().ToQueryString(); got != "{}" {
		t.Errorf("NewFilters() = %q; want {}", got)
	}
}

// D16: a change to http.DefaultTransport still applies when no client is
// supplied. (This passes on v1.1.0 too: it guards the fix.)
func TestD16_HTTPDefaultTransportStillApplies(t *testing.T) {
	srv := fakeDADI(t, issue("t", 3600), answer(`{}`))
	orig := http.DefaultTransport
	var used atomic.Int32
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used.Add(1)
		return orig.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = orig })

	if _, _, err := newTestInstance(t, srv).Do(context.Background(), &Get{Collection: "books"}, nil); err != nil {
		t.Errorf("Do: %v", err)
	}
	if n := used.Load(); n != 2 {
		t.Errorf("%d request(s) went through http.DefaultTransport; want 2 (token and API)", n)
	}
}

// D10: the escaping keeps what v1.1.0 sent for every value that worked in
// v1.1.0. A Collection of "books/<id>" was v1.1.0's way to get one
// document, and a value its caller had percent-encoded went out unchanged:
// both still do, so nothing reaches the server encoded twice. (This passes
// on v1.1.0 too.)
func TestD10_WhatV110SentIsSentUnchanged(t *testing.T) {
	type seen struct{ requestURI, path string }
	var mu sync.Mutex
	var last seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = seen{r.RequestURI, r.URL.Path}
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
		{"one document through the collection", testProperty, &Get{Collection: "books/abc123"},
			"/1.0/library/books/abc123", "/1.0/library/books/abc123"},
		{"its count", testProperty, &Get{Collection: "books/abc123", Metadata: true},
			"/1.0/library/books/abc123/count", "/1.0/library/books/abc123/count"},
		{"an ID with an encoded slash", testProperty, &Put{Collection: "books", ID: "a%2Fb"},
			"/1.0/library/books/a%2Fb", "/1.0/library/books/a/b"},
		{"an ID with an encoded percent sign", testProperty, &Post{Collection: "offers", ID: "50%25off"},
			"/1.0/library/offers/50%25off", "/1.0/library/offers/50%off"},
		{"a property with an encoded space", "1.0/my%20library", &Get{Collection: "books"},
			"/1.0/my%20library/books", "/1.0/my library/books"},
		{"an ID with a space", testProperty, &Delete{Collection: "books", ID: "x y"},
			"/1.0/library/books/x%20y", "/1.0/library/books/x y"},
		{"an ID with unicode", testProperty, &Delete{Collection: "books", ID: "caf\u00e9"},
			"/1.0/library/books/caf%C3%A9", "/1.0/library/books/caf\u00e9"},
		{"a collection with a trailing slash", testProperty, &Get{Collection: "books/"},
			"/1.0/library/books/", "/1.0/library/books/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.req.Initialize(proto, host, tc.property, "token", port); err != nil {
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
				t.Errorf("sent %s (decoded path %q); want %s (decoded %q)", got.requestURI, got.path, tc.wantURI, tc.wantPath)
			}
		})
	}
}
