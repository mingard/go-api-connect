// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const (
	requestTimeout       = 30
	Regex                = "$regex"
	LessThan             = "$lt"
	LessThanOrEqualTo    = "$lte"
	GreaterThan          = "$gt"
	GreaterThanOrEqualTo = "$gte"
	Equals               = "$eq"
	Not                  = "$not"
	NotEquals            = "$ne"
	NotIn                = "$nin"
	In                   = "$in"
	Now                  = "$now"
)

// jsonString returns s as a JSON string: quotes, backslashes and control
// characters are escaped, while <, > and & are left as they are, so that a
// regular expression stays readable.
func jsonString(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	return strings.TrimSuffix(b.String(), "\n")
}

// condition returns `"field": {"operator": value}`, the format of v1.1.0,
// with the field and the operator encoded as JSON strings and value as given.
func condition(field, operator, value string) string {
	return jsonString(field) + ": {" + jsonString(operator) + ": " + value + "}"
}

// FieldFilter is an interface for a filter.
//
// The filters of this package write every field name, operator and string
// value as a JSON string, so input cannot add conditions to a query.
type FieldFilter interface {
	// String returns a stringified filter.
	String() string
}

// FieldExistsFilter stores exists filter operator and value.
type FieldExistsFilter struct {
	Field    string
	Operator string
	Value    bool
}

// String returns a fieldexists filter.
func (f *FieldExistsFilter) String() string {
	return condition(f.Field, f.Operator, "null")
}

// FieldExists returns a new instance of FieldExistsFilter.
func FieldExists(f string, v bool) FieldFilter {
	var o string
	if v {
		o = NotEquals
	} else {
		o = Equals
	}
	return &FieldExistsFilter{
		Field:    f,
		Operator: o,
		Value:    v,
	}
}

// BooleanFilter stores boolean operator and value.
type BooleanFilter struct {
	Field    string
	Operator string
	Value    bool
}

// String returns a stringified filter.
func (b *BooleanFilter) String() string {
	return condition(b.Field, b.Operator, strconv.FormatBool(b.Value))
}

// BoolFilter returns a new instance of BooleanFilter.
func BoolFilter(f, o string, v bool) FieldFilter {
	return &BooleanFilter{
		Field:    f,
		Operator: o,
		Value:    v,
	}
}

// NumberFilter stores number operator and value.
type NumberFilter struct {
	Field    string
	Operator string
	Value    uint64
}

// String returns a stringified filter.
func (n *NumberFilter) String() string {
	return condition(n.Field, n.Operator, strconv.FormatUint(n.Value, 10))
}

// NumFilter returns a new instance of NumberFilter.
func NumFilter(f, o string, v uint64) FieldFilter {
	return &NumberFilter{
		Field:    f,
		Operator: o,
		Value:    v,
	}
}

// OneOfFilter defines a multi value check.
type OneOfFilter struct {
	Field    string
	Operator string
	Values   []string
}

// String returns a stringified filter. Empty values are left out, and a
// list with no values is written [""], as in v1.1.0.
func (o *OneOfFilter) String() string {
	values := make([]string, 0, len(o.Values))
	for _, v := range o.Values {
		if v != "" {
			values = append(values, jsonString(v))
		}
	}
	if len(values) == 0 {
		values = append(values, `""`)
	}
	return condition(o.Field, o.Operator, "["+strings.Join(values, ",")+"]")
}

// IsOneOf returns a new instance of OneOfFilter.
func IsOneOf(f string, v []string) FieldFilter {
	return &OneOfFilter{
		Field:    f,
		Operator: In,
		Values:   v,
	}
}

// StringFilter stores number operator and value.
type StringFilter struct {
	Field    string
	Operator string
	Value    string
}

// String returns a stringified filter.
func (s *StringFilter) String() string {
	return condition(s.Field, s.Operator, jsonString(s.Value))
}

// StrFilter returns a new instance of StringFilter. The field, the operator
// and the value are escaped as JSON strings by the library, so pass them
// unescaped: a regular expression such as `^\d+$` as it is.
func StrFilter(f, o, v string) FieldFilter {
	return &StringFilter{
		Field:    f,
		Operator: o,
		Value:    v,
	}
}

// Filters is a wrapper for field filters.
type Filters struct {
	Value []FieldFilter
}

// ToQueryString converts filter to a query string.
func (f *Filters) ToQueryString() string {
	if f.Value == nil {
		return ""
	}

	filters := make([]string, 0)

	for _, f := range f.Value {
		filters = append(filters, f.String())
	}

	return fmt.Sprintf(`{%s}`, Join(",", 1, filters...))
}

// NewFilters returns a new instance of Filters.
func NewFilters(filters ...FieldFilter) *Filters {
	f := &Filters{
		Value: make([]FieldFilter, 0),
	}

	f.Value = append(f.Value, filters...)
	return f
}
