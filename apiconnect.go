// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import (
	"context"
	"encoding/json"
	"net/http"
)

// Instance stores connection parameters.
type Instance struct {
	Proto    string
	Hostname string
	Port     int
	Property string
	wallet   *Wallet
}

// Do executes a request.
func (i *Instance) Do(ctx context.Context, r Request, res interface{}) ([]byte, *http.Header, error) {
	bearer := i.wallet.GetBearer(ctx)
	if err := r.Initialize(i.Proto, i.Hostname, i.Property, bearer, i.Port); err != nil {
		return nil, nil, err
	}

	resp, headers, err := r.Do(ctx)
	if err != nil || resp == nil {
		return nil, headers, err
	}

	err = json.Unmarshal(resp, &res)
	return resp, headers, err
}

// GetBearer proxy the call to the private field `wallet`.
func (i *Instance) GetBearer(ctx context.Context) string {
	return i.wallet.GetBearer(ctx)
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
