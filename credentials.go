// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import "encoding/json"

// Credentials stores auth credentials.
type Credentials struct {
	ClientID string `json:"clientId"`
	Secret   string `json:"secret"`
}

// ToJSON marshals credentials to json string.
func (c *Credentials) ToJSON() ([]byte, error) {
	return json.Marshal(c)
}
