// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import (
	"encoding/json"
	"time"
)

// renewBefore specifies the duration before expiry to renew a bearer token.
const renewBefore = time.Duration(60 * time.Second)

// Bearer stores bearer credentials.
type Bearer struct {
	ExpiresIn      int `json:"expiresIn,omitempty"`
	ExpirationDate time.Time
	Value          string `json:"accessToken,omitempty"`
}

// IsExpired checks that the bearer is valid.
func (b *Bearer) IsExpired() bool {
	return b.ExpirationDate.Before(time.Now().Add(renewBefore))
}

// NewBearerFromJSON decodes json values and returns a bearer.
func NewBearerFromJSON(d []byte) (*Bearer, error) {
	bearer := new(Bearer)
	if err := json.Unmarshal(d, &bearer); err != nil {
		return nil, err
	}

	// Convert expiry to timestamp.
	bearer.ExpirationDate = time.Now().Add(time.Second * time.Duration(bearer.ExpiresIn))

	return bearer, nil
}
