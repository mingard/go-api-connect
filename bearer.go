// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"
)

// renewBefore is the longest a bearer token is renewed before it expires. A
// token that lives less than twice as long is renewed halfway through its
// life instead.
const renewBefore = 60 * time.Second

// Bearer stores bearer credentials.
//
// Printing a Bearer with fmt, whatever the verb, or logging it with
// log/slog, shows its expiry but never its token. Redaction goes through
// the methods of Bearer, so it has two limits: fmt cannot call a method on a
// Bearer in an unexported struct field, and prints it in full; and slog's
// JSON handler encodes a Bearer inside a slice, a map or a struct with
// encoding/json, token included.
type Bearer struct {
	ExpiresIn      int `json:"expiresIn,omitempty"`
	ExpirationDate time.Time
	Value          string `json:"accessToken,omitempty"`
}

// IsExpired reports whether the bearer is due for renewal: whether it
// expires within its renewal margin. The margin is 60 seconds, or half the
// token's lifetime (ExpiresIn) when that is shorter, so a short-lived token
// is used for the first half of its life. A Bearer with no ExpiresIn keeps
// the 60-second margin.
func (b *Bearer) IsExpired() bool {
	return b.ExpirationDate.Before(time.Now().Add(b.renewMargin()))
}

// renewMargin returns how long before ExpirationDate the bearer is renewed.
func (b *Bearer) renewMargin() time.Duration {
	const short = int(2 * renewBefore / time.Second) // seconds; a shorter life is halved
	if b.ExpiresIn > 0 && b.ExpiresIn < short {
		return time.Duration(b.ExpiresIn) * time.Second / 2
	}
	return renewBefore
}

// NewBearerFromJSON decodes json values and returns a bearer. Its
// ExpirationDate is ExpiresIn seconds from now; an ExpiresIn beyond the
// range of a time.Duration, about 292 years, counts as 292 years.
func NewBearerFromJSON(d []byte) (*Bearer, error) {
	bearer := new(Bearer)
	if err := json.Unmarshal(d, bearer); err != nil {
		return nil, err
	}

	// Convert expiry to timestamp.
	bearer.ExpirationDate = time.Now().Add(lifetime(bearer.ExpiresIn))

	return bearer, nil
}

// lifetime returns expiresIn seconds as a Duration. Beyond the range of a
// Duration, about 292 years either way, it returns the largest or smallest
// Duration rather than wrapping around.
func lifetime(expiresIn int) time.Duration {
	const most = int64(math.MaxInt64 / time.Second) // 9223372036 seconds
	switch s := int64(expiresIn); {
	case s > most:
		return math.MaxInt64
	case s < -most:
		return math.MinInt64
	default:
		return time.Duration(s) * time.Second
	}
}

// String returns the bearer with its token redacted.
func (b Bearer) String() string {
	return fmt.Sprintf("{ExpiresIn:%d ExpirationDate:%s Value:%s}",
		b.ExpiresIn, b.ExpirationDate.Format(time.RFC3339), redact(b.Value))
}

// GoString returns the bearer as Go syntax, for the %#v verb, with its token
// redacted.
func (b Bearer) GoString() string {
	return fmt.Sprintf("apiconnect.Bearer{ExpiresIn:%d, ExpirationDate:%#v, Value:%q}",
		b.ExpiresIn, b.ExpirationDate, redact(b.Value))
}

// Format implements fmt.Formatter, so that every verb redacts the token:
// %#v prints GoString, and any other verb String.
func (b Bearer) Format(f fmt.State, verb rune) {
	formatRedacted(f, verb, b.String(), b.GoString())
}

// LogValue implements slog.LogValuer: it logs the bearer's expiry and a
// redacted token.
func (b Bearer) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("expiresIn", b.ExpiresIn),
		slog.Time("expirationDate", b.ExpirationDate),
		slog.String("accessToken", redact(b.Value)),
	)
}
