// Arthur Mingard
// (c) 2022 Arthur Mingard

// Package apiconnect is a client for DADI API instances. An Instance gets a
// bearer token from the instance's /token endpoint with a client id and
// secret, renews it before it expires, and sends Get, Post, Put and Delete
// requests to the instance's collections.
//
// Use https. The token request carries the client secret and every API
// request carries the bearer token, so over http anyone on the network path
// can read them. Use http only on loopback or a network you trust.
package apiconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Instance stores connection parameters.
//
// Create an Instance with New. An Instance built as a struct literal has no
// credentials, so Do and Token return an error wrapping ErrToken. An
// Instance is safe for concurrent use.
type Instance struct {
	Proto    string
	Hostname string
	Port     int
	Property string
	wallet   *Wallet

	// HTTPClient sends this Instance's requests, the token request
	// included: set it for a private CA, a proxy, mutual TLS or tracing.
	// When nil, a client of the library's own on http.DefaultTransport is
	// used; http.DefaultClient never is. HTTPClient is never modified: each
	// request goes through a copy with the library's redirect policy (see
	// Do), which runs before HTTPClient's own CheckRedirect. Set it before
	// the first request.
	//
	// HTTPClient and MaxBodySize apply to the token request and to this
	// package's Get, Post, Put and Delete, including types of yours that
	// embed them. A Request type of your own sends itself, through whatever
	// client its Do uses, so neither applies to it.
	HTTPClient *http.Client

	// MaxBodySize is the largest response body, in bytes, that Do reads. A
	// longer body is an error wrapping ErrBodyTooLarge, never a truncated
	// body. Zero or less means DefaultMaxBodySize. Set it before the first
	// request.
	MaxBodySize int64
}

// Do sends r with a bearer token and returns the response body and headers.
// When res is not nil, Do decodes the JSON body into it, so res must then be
// a non-nil pointer; when res is nil, Do decodes nothing.
//
// When no token can be obtained, Do sends nothing and returns an error
// wrapping ErrToken. A response other than 200, 204 or 304 is an error, and
// a 204 or 304 has no body. The token request never follows a redirect. An
// API request follows one only to the same host and port, never from https
// to another scheme, and never by sending a POST, PUT or DELETE again as a
// GET: a refused redirect is an error wrapping ErrRedirect.
func (i *Instance) Do(ctx context.Context, r Request, res any) ([]byte, *http.Header, error) {
	if r == nil {
		return nil, nil, errors.New("apiconnect: Do needs a Request")
	}
	bearer, err := i.Token(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := r.Initialize(i.Proto, i.Hostname, i.Property, bearer, i.Port); err != nil {
		return nil, nil, err
	}
	if hr, ok := r.(httpRequester); ok {
		if h := hr.httpRequest(); h != nil {
			h.Client = i.HTTPClient
			h.MaxBodySize = i.MaxBodySize
		}
	}

	resp, headers, err := r.Do(ctx)
	if err != nil || resp == nil {
		return nil, headers, err
	}

	if res != nil {
		err = json.Unmarshal(resp, res)
	}
	return resp, headers, err
}

// Token returns a valid bearer token for this Instance, asking the token
// endpoint for one when needed. Concurrent callers share one token request,
// which carries on, for up to 30 seconds, after the context of the caller
// that started it ends. Every error wraps ErrToken; see Wallet.Token.
func (i *Instance) Token(ctx context.Context) (string, error) {
	if i == nil || i.wallet == nil {
		return "", fmt.Errorf("%w: the Instance has no credentials (create it with New)", ErrToken)
	}
	return i.wallet.token(ctx, i.HTTPClient)
}

// GetBearer returns a valid bearer token, or "" when none can be obtained.
// Token says why.
func (i *Instance) GetBearer(ctx context.Context) string {
	tok, _ := i.Token(ctx)
	return tok
}

// New returns a new instance of API.
func New(proto, host, property, client, secret string, port int) *Instance {
	return &Instance{
		Proto:    proto,
		Hostname: host,
		Port:     port,
		Property: property,
		wallet:   NewWallet(proto, host, client, secret, port),
	}
}

// httpRequester is implemented by this package's request types, so that Do
// can pass them the Instance's HTTPClient and MaxBodySize.
type httpRequester interface {
	httpRequest() *HTTPRequest
}
