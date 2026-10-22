# API Connect
[![godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://godoc.org/github.com/mingard/go-api-connect)
[![Go Report Card](https://goreportcard.com/badge/mingard/go-api-connect)](https://goreportcard.com/report/mingard/go-api-connect)
[![Coverage](https://gocover.io/_badge/github.com/mingard/go-api-connect)](https://gocover.io/github.com/mingard/go-api-connect)

A simple easy way to connect go DADI API instances. It gets a bearer token
from the instance's `/token` endpoint with a client id and secret, renews it
before it expires, and sends `Get`, `Post`, `Put` and `Delete` requests to the
instance's collections. It uses the standard library only.

## Install

```
go get github.com/mingard/go-api-connect
```

Requires Go 1.27.3 or later.

## Use https

**Use `https`.** The token request carries your client secret, and every API
request carries the bearer token. Over `http` both travel in plain text, and
anyone on the network path can read them. Use `http` only on loopback or on a
network you trust.

The library also refuses redirects that would send them elsewhere. The token
request never follows a redirect. An API request follows one only to the same
host and port, and never from `https` to `http`. Nor does it follow a
redirect that changes its method: net/http sends a `POST`, `PUT` or `DELETE`
that is answered `301`, `302` or `303` again as a `GET`, without its body,
which would lose the write. A `307` or `308` keeps the method and the body,
and is followed. A refused redirect is an error wrapping `ErrRedirect`.

## Usage

```go
import apiconnect "github.com/mingard/go-api-connect"

// The property is the API version and the database: /1.0/library/books.
api := apiconnect.New("https", "api.example.com", "1.0/library", clientID, secret, 443)
```

`New` does not contact the server. The first request fetches a token, which is
reused and renewed before it expires: 60 seconds before, or halfway through
its life for a token that lives less than two minutes. Concurrent requests
share one token request, and an `Instance` is safe for concurrent use.

### Read a collection

```go
type Book struct {
	ID    string `json:"_id,omitempty"`
	Title string `json:"title"`
	Year  int    `json:"year"`
}

// Books from 1965 on whose title starts with "Dune", newest first.
get := &apiconnect.Get{
	Collection: "books",
	Limit:      20,
	Sort:       apiconnect.NewSort().Desc("year").Asc("title"),
	Query: apiconnect.NewFilters(
		apiconnect.StrFilter("title", apiconnect.Regex, "^Dune"),
		apiconnect.NumFilter("year", apiconnect.GreaterThanOrEqualTo, 1965),
	),
}
var page struct {
	Results []Book `json:"results"`
}
body, headers, err := api.Do(ctx, get, &page)
```

`Do` decodes the JSON body into its last argument, which must be a pointer.
Pass `nil` to get only the raw body. Set `ID` to get one document
(`/1.0/library/books/<id>`), or `Metadata: true` to ask for the collection's
`/count`. Set `ETag` to make a conditional request: a `304 Not Modified`
returns no body and no error.

The library writes every filter and sort field name, operator and value as an
escaped JSON string, so pass them unescaped, for example
``StrFilter("isbn", Regex, `^\d{13}$`)``. Sort fields keep the order of the
`Asc` and `Desc` calls, and the first one takes precedence.

### Create, update and delete

```go
book, _ := json.Marshal(Book{Title: "Children of Dune", Year: 1976})
_, _, err := api.Do(ctx, &apiconnect.Post{Collection: "books", Body: book}, nil)

update := []byte(`{"title":"Children of Dune (first edition)"}`)
_, _, err = api.Do(ctx, &apiconnect.Put{Collection: "books", ID: id, Body: update}, nil)

_, _, err = api.Do(ctx, &apiconnect.Delete{Collection: "books", ID: id}, nil)
```

The property and the collection keep their slashes, and each of their
segments is escaped. An ID is always one path segment: `/`, `?`, `#` and the
like are escaped, so an ID such as `50%off` reaches the server as `50%off`.
A valid escape you wrote yourself, such as `%2F`, is sent as it is, as in
1.1.0, so nothing is encoded twice. To get one document, set `Get.ID`, or,
as in 1.1.0, use a `Collection` of `books/<id>`. A `Delete` with no `ID` is
an error.

DADI API answers a `DELETE` with `204 No Content` unless its `feedback`
setting is on. `Do` returns no body and no error for a `204`.

### Errors

Any response other than `200`, `204` or `304` is an error, such as
`API request failed with code 404`. Test for the library's own errors with
`errors.Is`:

| Error | Meaning |
| --- | --- |
| `ErrToken` | No bearer token: the token request failed or was refused, its answer held no usable token, or the context ended first. `Do` sends nothing. |
| `ErrRedirect` | A redirect was refused (see [Use https](#use-https)). |
| `ErrBodyTooLarge` | The response body was over the limit. |
| `ErrNotInitialized` | A request was sent or marshalled before `Initialize` succeeded. |

Each error wraps its cause. `errors.As` still finds a `*url.Error` or a
`*json.SyntaxError`, and `errors.Is` finds `context.DeadlineExceeded`.
`api.Token(ctx)` returns the current token, or the reason there is none.

### Your own HTTP client

By default the library sends through a client of its own on
`http.DefaultTransport`. It never uses `http.DefaultClient`. For a private CA,
a proxy, mutual TLS or tracing, set `HTTPClient` before the first request:

```go
api.HTTPClient = &http.Client{Transport: myTransport}
```

That client sends the token request too. The library never modifies it: each
request goes through a copy whose redirect policy runs before the client's own
`CheckRedirect`. A `CheckRedirect` of yours then decides how many redirects to
follow, as in net/http; without one, the limit is 10.

`HTTPClient` and `MaxBodySize` apply to the token request and to this
package's `Get`, `Post`, `Put` and `Delete`, including types of yours that
embed them. A `Request` type of your own sends itself, through whatever client
its `Do` uses, so neither applies to it.

### Limits

An API request times out after 30 seconds, or sooner if its context ends.
The token request is shared: each caller stops waiting when its own context
ends, but the request carries on, for up to 30 seconds, so that the other
callers can use its token. So it can outlive the context of the caller that
started it, for example during a graceful shutdown. While the current token
has not yet expired, a caller whose context ends first still gets it.

A response body over 64 MiB (`DefaultMaxBodySize`) is an error, never a
truncated body. To change the limit, set `MaxBodySize` on the `Instance`. A
token response is capped at 1 MiB.

### Logging

The library never prints or logs. If you print `Credentials`, a `Bearer`, a
`Wallet` or an `HTTPRequest` with `fmt`, whatever the verb, or log
`Credentials` or a `Bearer` with `log/slog`, the secret and the token are
redacted. A `Wallet` prints its token URL and client id, and an `HTTPRequest`
its method and URL, never its headers.

Redaction goes through their methods, so it has limits:

- `fmt` cannot call a method on an unexported struct field, so `Credentials`
  or a `Bearer` held in one print in full.
- slog's JSON handler encodes a value inside a slice, a map or a struct with
  `encoding/json`, which writes the secret and the token, as the token request
  needs. Log `Credentials` and a `Bearer` as attributes of their own.
- The methods are promoted, so a struct of yours that embeds `Credentials` or a
  `Bearer` prints and logs as just the credentials or the bearer.

## Changes

See [CHANGELOG.md](CHANGELOG.md).
