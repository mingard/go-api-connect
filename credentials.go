// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// redacted replaces a secret or a token in printed and logged values.
const redacted = "[redacted]"

// redact returns redacted for a secret that is set, and "" for one that is
// not, so that a missing secret still shows.
func redact(secret string) string {
	if secret == "" {
		return ""
	}
	return redacted
}

// formatRedacted writes a value with its secrets redacted, whatever the
// verb: gs for %#v, and s for any other verb. The verbs that format a
// string, v, s, q, x and X, format s with their flags, width and precision.
func formatRedacted(f fmt.State, verb rune, s, gs string) {
	switch {
	case verb == 'v' && f.Flag('#'):
		io.WriteString(f, gs)
	case strings.ContainsRune("vsqxX", verb):
		fmt.Fprintf(f, fmt.FormatString(f, verb), s)
	default:
		io.WriteString(f, s)
	}
}

// Credentials stores auth credentials.
//
// Printing Credentials with fmt, whatever the verb, or logging them with
// log/slog, shows the client id but never the secret. Redaction goes
// through the methods of Credentials, so it has two limits: fmt cannot call
// a method on Credentials in an unexported struct field, and prints them in
// full; and slog's JSON handler encodes Credentials inside a slice, a map or
// a struct with encoding/json. JSON encoding, which ToJSON uses for the
// token request, includes the secret.
type Credentials struct {
	ClientID string `json:"clientId"`
	Secret   string `json:"secret"`
}

// ToJSON marshals credentials to json string.
func (c *Credentials) ToJSON() ([]byte, error) {
	return json.Marshal(c)
}

// String returns the credentials with the secret redacted.
func (c Credentials) String() string {
	return fmt.Sprintf("{ClientID:%s Secret:%s}", c.ClientID, redact(c.Secret))
}

// GoString returns the credentials as Go syntax, for the %#v verb, with the
// secret redacted.
func (c Credentials) GoString() string {
	return fmt.Sprintf("apiconnect.Credentials{ClientID:%q, Secret:%q}", c.ClientID, redact(c.Secret))
}

// Format implements fmt.Formatter, so that every verb redacts the secret:
// %#v prints GoString, and any other verb String.
func (c Credentials) Format(f fmt.State, verb rune) {
	formatRedacted(f, verb, c.String(), c.GoString())
}

// LogValue implements slog.LogValuer: it logs the client id and a redacted
// secret.
func (c Credentials) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("clientId", c.ClientID),
		slog.String("secret", redact(c.Secret)),
	)
}
