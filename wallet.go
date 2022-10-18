// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
)

// Wallet stores wallet credentials.
type Wallet struct {
	credentials *Credentials
	url         string
	bearer      *Bearer
}

// NewBearer gets a new bearer token from the token endpoint.
func (w *Wallet) NewBearer(ctx context.Context) {
	data, err := w.credentials.ToJSON()

	if err != nil {
		fmt.Println("ERROR", err)
	}

	r := &HTTPRequest{
		Method:  http.MethodPost,
		URL:     w.url,
		Payload: bytes.NewBuffer(data),
	}

	if err := r.Initialize(); err != nil {
		return
	}

	r.SetHeader("Content-Type", "application/json")
	res, _, err := r.Do(ctx)

	if err != nil {
		return
	}

	// Decode bearer.
	if bearer, err := NewBearerFromJSON(res); err == nil {
		w.bearer = bearer
	}
}

// GetBearer returns a valid bearer token.
func (w *Wallet) GetBearer(ctx context.Context) string {
	// Check for bearer
	if w.bearer == nil || w.bearer.IsExpired() {
		w.NewBearer(ctx)
	}
	if w.bearer != nil {
		return w.bearer.Value
	}

	return ""
}

// NewWallet creates a new instance of Wallet.
func NewWallet(proto, host, client, secret string, port int) *Wallet {
	return &Wallet{
		url:         fmt.Sprintf("%s://%s:%d/token", proto, host, port),
		credentials: &Credentials{client, secret},
	}
}
