# Changelog

All notable changes to this module are recorded here. Versions follow
[Semantic Versioning](https://semver.org/).

## 1.2.0 (2026-10-22)

A security and reliability release. It is a minor release. No exported
identifier, signature or struct field of 1.1.0 is removed or changed, and
`Get` still embeds `*Sort`. New API is listed under *Added*. Every change of
behaviour is listed under *Changed*, with what 1.1.0 did. Each one fixes a
defect of 1.1.0, or follows from such a fix and says how to adapt.

### Security

- **The token request no longer follows redirects.** In 1.1.0, an `https`
  token endpoint that answered `307` to another host made the library post the
  client id and secret there, in plain text over `http`, and use the token
  that host returned.
- **API requests follow a redirect only to the same host and port, and never
  from `https` to another scheme.** In 1.1.0, a redirect from `https` to
  `http` on the same host name, or to another port, sent
  `Authorization: Bearer <token>` there.
- **Filter and sort input is escaped.** Field names, operators, string values,
  `IsOneOf` values and sort fields are now written as JSON strings. In 1.1.0,
  input containing a quote could add conditions to the query:
  ``StrFilter("email", Equals, `x"}, "password": {"$ne": "`)`` produced
  `{"email": {"$eq": "x"}, "password": {"$ne": ""}}`.
- **URLs are built with escaping.** In 1.1.0, an ID containing `/`, `?` or `#`
  changed the path or the query of the request.
- **Printing or logging `Credentials` or a `Bearer` no longer shows the secret
  or the token**, and printing a `Wallet` or an `HTTPRequest` no longer shows
  them either (`fmt`, whatever the verb; `log/slog`). In 1.1.0, `%s` on a
  `*Wallet` showed the client secret and the token, and on an `HTTPRequest`
  its `Authorization: Bearer <token>` header. See *Printing and logging*
  under *Changed* for the limits.
- **Response bodies are capped**, at 64 MiB by default and 1 MiB for the token
  response. In 1.1.0, a response was read into memory whatever its size.
- **The library no longer sends through `http.DefaultClient`**, which any
  other package can reconfigure.
- The README now says to use `https`, and to use `http` only on loopback or a
  trusted network: over `http` the client secret and the bearer token travel
  in plain text. `http` still works.

### Fixed

- A data race on token renewal. Concurrent callers now share a single token
  request, both on the first call and at each renewal. In 1.1.0, 16
  concurrent first calls made 16 token requests and the race detector
  reported races on the token.
- A token failure was swallowed and the request went out with
  `Authorization: Bearer `. `Do` now returns the token error and sends
  nothing.
- A token that lives 60 seconds or less counted as expired as soon as it
  arrived, so every call fetched a new one. It is now renewed halfway through
  its life.
- A token whose `expiresIn` is 9223372037 seconds or more (about 292 years)
  overflowed `time.Duration` and counted as expired at once, so every call
  fetched a new one. Such a lifetime now counts as about 292 years, and a
  hugely negative `expiresIn` given to `NewBearerFromJSON` no longer wraps
  around into the future.
- The body of an error response (any status but `200`, `204` or `304`) was
  never closed, so every error left its connection unusable. It is now drained (up
  to 64 KiB, for at most 50 ms) and closed on every path, and the connection
  is reused.
- A `DELETE` answered `204 No Content`, which DADI API sends unless its
  `feedback` setting is on, was reported as `API request failed with code
  204`. A `204` is now a success with no body.
- A `POST`, `PUT` or `DELETE` answered with a `301`, `302` or `303` redirect
  was sent again, as net/http does, as a `GET` without its body, and `Do`
  reported success with the answer to the `GET`: the write was lost. Such a
  redirect is now refused.
- Panics, now returned as errors or handled:
  - `HTTPRequest.Initialize` with a URL that does not parse, for example a
    `Delete` with the ID `50%off`, or `New("", ...)`.
  - `MarshalBinary` or `Do` on a request before `Initialize`.
  - `Do`, `GetBearer` and `Token` on an `Instance` built as a struct literal,
    or on a nil `Instance` or `Wallet`.
  - Printing a `Get` with no sort, which showed
    `%!v(PANIC=String method: ... nil pointer dereference)`.
  - `Asc` and `Desc` on a `Sort` whose map is nil.
  - A token endpoint that answers `null`, and `NewBearerFromJSON([]byte("null"))`.
  - `Do` with a nil `Request`.
- A multi-field sort came out in random order: the precedence of
  `Asc("publishedAt").Desc("title")` changed from one call to the next.
- A sort value was printed in binary (`%b`), so any value but 1 or -1 was
  wrong.
- An IPv6 host such as `::1` gave an invalid URL, and one with a zone, such as
  `fe80::1%en0`, panicked.
- The library printed `ERROR ...` to standard output. It no longer prints or
  logs anything.
- `Get` always sent `If-None-Match`, empty when there was no ETag.
- `Do` silently ignored a non-pointer `res`, and decoded into a value it then
  discarded when `res` was nil.
- A `Delete` with no ID was sent to the collection URL with a trailing slash.

### Added

- `func (*Instance) Token(ctx context.Context) (string, error)` and
  `func (*Wallet) Token(ctx context.Context) (string, error)` return the
  token, or why there is none. `GetBearer` keeps returning `""` on failure.
- `Instance.HTTPClient`, `Wallet.HTTPClient` and `HTTPRequest.Client` let you
  supply your own `*http.Client`, for example for a private CA, a proxy,
  mutual TLS or tracing. The library never modifies it.
- `Instance.MaxBodySize`, `HTTPRequest.MaxBodySize` and
  `DefaultMaxBodySize` (64 MiB).
- `ErrToken`, `ErrRedirect`, `ErrBodyTooLarge` and `ErrNotInitialized`, for
  `errors.Is`. Each is wrapped with its cause, so `errors.As` still reaches a
  `*url.Error` or a `*json.SyntaxError`.
- `String`, `GoString`, `Format` (`fmt.Formatter`) and `LogValue` (for
  `log/slog`) on `Credentials` and `Bearer`. They show the client id and the
  expiry, never the secret or the token.
- `Format` on `Wallet` and `HTTPRequest`. A `Wallet` prints its token URL and
  client id, and an `HTTPRequest` its method and URL.
- `Get.ID`, to get one document: `/1.0/library/books/<id>`. A `Collection` of
  `books/<id>`, the way to do it in 1.1.0, still works.
- A Usage section in the README with a compiled example (`example_test.go`),
  tests for every fix, and a CI workflow (`gofmt`, `go vet`,
  `go test -race`, `govulncheck`).

### Changed

These are all behaviour changes; signatures are unchanged.

- **`Do` with no token.** When no token can be obtained, `Do` returns an error
  wrapping `ErrToken` and sends nothing. In 1.1.0 it sent the request with
  `Authorization: Bearer ` and reported only the server's answer, for
  example `API request failed with code 401`.
- **Token responses are checked.** An empty or missing `accessToken`, a
  missing `expiresIn` or one of 0 or less, a body that is not JSON, and
  `null` are each an error wrapping `ErrToken`. In 1.1.0 an empty token, or
  a token with such an `expiresIn`, was stored and sent; a body that is not
  JSON was ignored, so the request went out with the previous token, or
  with `Authorization: Bearer ` and no token when there was none; and
  `null` panicked. The token response is read up to 1 MiB.
- **Renewal margin.** `Bearer.IsExpired` renews a token 60 seconds before it
  expires, or halfway through its life when it lives less than two minutes. A
  `Bearer` with no `ExpiresIn` keeps the 60-second margin.
- **A failed renewal.** If a renewal fails, or the caller's context ends
  first, while the current token has not yet expired, that token is still
  used. Once it has expired, the failure is an error. In 1.1.0 the old token
  was used even after it had expired.
- **One token request at a time.** Concurrent callers wait for the same token
  request, and each stops waiting when its own context ends: it gets the
  current token if that has not yet expired, or else an error wrapping
  `ErrToken` and `ctx.Err()`. The request itself carries on, for up
  to the 30-second request timeout, so a caller that gives up does not fail
  it for the others. A failure is not remembered: the next call asks again.
  `NewBearer` joins a token request already in progress, and starts none when
  its context has already ended. The request runs on a goroutine of the
  library's: if your transport panics while it sends it, each caller waiting
  for it panics with the same value, as the caller did in 1.1.0, which sent
  the request itself. A panic with no caller left waiting is dropped. If the
  transport calls `runtime.Goexit`, as `t.Fatal` does, the waiting callers
  get an error wrapping `ErrToken`. Either way the next call asks again.
- **Redirects.** The token request never follows a redirect. An API request
  follows one only to the same host and port, never from `https` to another
  scheme, and never by changing its method. net/http sends a `POST`, `PUT` or
  `DELETE` that is answered `301`, `302` or `303` again as a `GET` with no
  body, and in 1.1.0 `Do` then returned the answer to that `GET` as if the
  write had succeeded; that redirect is now refused. A `307` or `308` keeps
  the method and the body, and is followed. A refused redirect is an error
  wrapping `ErrRedirect`, and so is stopping after 10 redirects. A supplied
  client's own `CheckRedirect` still runs, after this policy, for the
  redirects it allows, and then decides how many redirects to follow, as in
  net/http; without one, the limit is 10.
- **HTTP client.** Requests go through a client of the library's own on
  `http.DefaultTransport`, or through `HTTPClient` when you set it. Changes to
  `http.DefaultClient` (its `Transport`, `Jar`, `CheckRedirect` or `Timeout`)
  no longer apply to this library. Changes to `http.DefaultTransport` still
  do.
- **Response size.** A body over `MaxBodySize` (64 MiB by default) is an error
  wrapping `ErrBodyTooLarge`, and none of it is returned. A `Content-Length`
  over the limit fails before the body is read.
- **Escaping in filters and sort.** Field names, operators, string values,
  `IsOneOf` values and sort fields are escaped as JSON strings (`<`, `>` and
  `&` are left as they are). Benign input gives exactly the 1.1.0 bytes. Input
  with a quote, a backslash or a control character is now escaped, so it
  reaches the server as written: a regular expression such as `^\d{13}$` is
  sent as `"^\\d{13}$"`, where 1.1.0 sent invalid JSON. **If you escaped quotes
  or backslashes yourself to work around 1.1.0, stop, or they will be escaped
  twice.** As `encoding/json` does, U+2028 and U+2029 are written as
  `\u2028` and `\u2029`, and invalid UTF-8 becomes U+FFFD.
- **Sort order.** `Sort.String` lists the fields added with `Asc` and `Desc`
  in the order of the calls, and then any fields written straight into
  `Value`, in sorted order. A field added again keeps its place and takes the
  new direction. Values print in decimal. A nil `*Sort` prints `{}`, so
  printing a `Get` with no sort shows `{}`.
- **Comparing a `Sort`.** To keep the order of the `Asc` and `Desc` calls, a
  `Sort` records it in a new unexported field. So a `Sort` built with `Asc`
  or `Desc`, or a `Get` that holds one, is no longer `reflect.DeepEqual` to
  one whose `Value` was written directly with the same fields; in 1.1.0 it
  was, since `Value` was all a `Sort` held. And go-cmp
  (`github.com/google/go-cmp`) now panics with `cannot handle unexported
  field` when `cmp.Equal` or `cmp.Diff` compares two `Sort` values, or two
  `Get` values that each hold one, even identical ones; in 1.1.0 it
  compared their `Value`. To compare as 1.1.0 did, ignoring the order, pass
  `cmpopts.IgnoreUnexported(apiconnect.Sort{})`. To compare the order too,
  compare the `String` of each `Sort`, which is the sort that is sent, or,
  with `reflect.DeepEqual`, build the expected `Sort` with the same `Asc`
  and `Desc` calls.
- **URLs.** The scheme, host and port are joined with `net.JoinHostPort`, so
  an IPv6 host is bracketed (`[::1]` still works too). A zone may be written
  `fe80::1%en0`, `[fe80::1%en0]`, or, as in 1.1.0, in URL syntax:
  `[fe80::1%25en0]`. The property and the collection keep their slashes
  (`1.0/library`; `books/<id>`), and each of their segments is escaped; an ID
  is always one segment. Escaping uses `url.PathEscape`, so `/` (in an ID),
  `?`, `#`, space, `;`, `,`, `(`, `)`, `!`, `*`, `'`, `[` and `]` are among the
  characters percent-encoded, and letters, digits and `$ & + - . : = @ _ ~`
  are left as they are. An empty protocol is an error.
  - **Whatever 1.1.0 sent, 1.2.0 sends unchanged.** A valid escape such as
    `%2F` or `%25` is kept as it is, as 1.1.0 sent it, so a value you
    percent-encoded yourself is not encoded twice (`a%2Fb` still reaches the
    server as `a/b`), and `Get{Collection: "books/<id>"}` still reaches the
    document. `Get.ID` is the new way to ask for one.
  - **What changes is input that 1.1.0 mishandled:** a `%` that starts no
    valid escape, such as `50%off`, panicked in 1.1.0 and is now sent as
    `50%25off`, which the server reads as `50%off`; a `?` or `#`, which moved
    the rest of the value into the query or the fragment, is encoded; a `/` in
    an ID, which added path segments, is encoded; and a `.` or `..` segment,
    which could climb the path, is written `%2E` or `%2E%2E`.
- **Errors instead of panics.** `HTTPRequest.Initialize` returns the error of
  a URL that does not parse. `MarshalBinary` and `Do` before `Initialize`
  return `ErrNotInitialized`. An `Instance` or `Wallet` with no credentials
  returns an error wrapping `ErrToken` from `Do` and `Token`, and `""` from
  `GetBearer`.
- **`Do` and `res`.** With a nil `res`, `Do` decodes nothing and returns the
  body, even one that is not JSON. In 1.1.0 it returned a JSON error for such
  a body. A non-pointer or nil-pointer `res` is an error
  (`*json.InvalidUnmarshalError`) returned with the body; in 1.1.0 it was
  ignored without an error.
- **`Delete` needs an ID.** `Delete.Initialize` with an empty ID is an error,
  and nothing is sent.
- **`If-None-Match`** is sent only when `Get.ETag` is set.
- **Error responses are closed.** The body of an error response is drained,
  up to 64 KiB and for at most 50 ms, and closed, so its connection can be
  reused. A body that is still arriving after 50 ms is given up, with its
  connection, so that a stalled body cannot hold `Do`.
- **`204 No Content`** is a success with no body, as `304` is: `Do` returns
  no body, the headers and no error. In 1.1.0 it was an error,
  `API request failed with code 204`.
- **`NewBearerFromJSON([]byte("null"))`** returns no error and the same
  `Bearer` as `{}`: no token, an `ExpiresIn` of 0, and an `ExpirationDate`
  of the time of the call. It used to panic. Decoding is otherwise
  unchanged.
- **Printing and logging.** `fmt`, whatever the verb, and `log/slog` show
  `Credentials` and `Bearer` with the secret and the token replaced by
  `[redacted]`; an empty secret or token shows as empty. `fmt` shows a
  `Wallet` as its token URL and client id, and an `HTTPRequest` as its method
  and URL. JSON encoding of `Credentials`, which is the token request body,
  and JSON decoding of `Bearer` are unchanged. Redaction goes through methods,
  so two cases still show the secret and the token: `Credentials` or a
  `Bearer` in an unexported struct field, on which `fmt` cannot call a
  method, and inside a slice, a map or a struct logged with slog's JSON
  handler, which encodes them with `encoding/json`. `String`, `GoString`,
  `Format` and `LogValue` are promoted, so a struct of yours that embeds
  `Credentials` or `Bearer` now prints with `fmt`, `%#v` included, and logs
  with `log/slog`, as just the credentials or the bearer: its other fields no
  longer appear.
- **A copy of a `Wallet` shares its token** and the token request in
  progress with the original; in 1.1.0 a copy renewed its token on its own.
  This follows from the shared token request: its lock sits behind a
  pointer, so a `Wallet` holds no lock by value, a copy taken while a token
  request is in progress keeps working, and copying one is clean for
  `go vet`.

## 1.1.0 (2026-10-21)

### Security

- Requires Go 1.27.3 or later; the `go` directive was 1.19. This is for
  CVE-2026-111943 / GO-2026-6809 in `crypto/x509`, which is reachable from
  this package: with Go 1.27.2, govulncheck traces `HTTPRequest.Do` through
  the `crypto/tls` client handshake to `crypto/x509.Certificate.Verify`. Go
  1.27.3 has the fix. There is no API change.

## 1.0.2 (2022-11-03)

- The request timeout is 30 seconds (was 5).

## 1.0.1 (2022-10-20)

- `Get.SkipCache` sends `cache=false`.

## 1.0.0 (2022-10-18)

- First release, with `Get.Metadata` for a collection's `/count`.
