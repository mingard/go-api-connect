// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import "errors"

// Errors to test for with errors.Is. The library wraps each one with detail,
// and with the underlying error when there is one, so errors.As still reaches
// a *url.Error or a *json.SyntaxError underneath.
var (
	// ErrToken means no bearer token could be obtained: the token request
	// failed or was refused, the answer held no usable token, or the
	// caller's context ended first. Do returns it without sending the
	// request.
	ErrToken = errors.New("apiconnect: no bearer token")

	// ErrRedirect means a redirect was refused. The token request never
	// follows a redirect. An API request follows one only to the same host
	// and port, never from https to another scheme, and never by changing
	// its method, as net/http does when it sends a POST, PUT or DELETE
	// answered 301, 302 or 303 again as a GET. Unless the HTTPClient's own
	// CheckRedirect decides, it stops after 10 redirects.
	ErrRedirect = errors.New("apiconnect: redirect refused")

	// ErrBodyTooLarge means a response body was longer than the limit
	// (MaxBodySize, or DefaultMaxBodySize). No part of the body is
	// returned.
	ErrBodyTooLarge = errors.New("apiconnect: response body too large")

	// ErrNotInitialized means a request was sent or marshalled before its
	// Initialize method had succeeded.
	ErrNotInitialized = errors.New("apiconnect: request not initialized")
)
