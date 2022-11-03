// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import "fmt"

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

// FieldFilter is an interface for a filter.
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
	return fmt.Sprintf(`"%s": {"%s": null}`, f.Field, f.Operator)
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
	return fmt.Sprintf(`"%s": {"%s": %t}`, b.Field, b.Operator, b.Value)
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
	return fmt.Sprintf(`"%s": {"%s": %d}`, n.Field, n.Operator, n.Value)
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

// String returns a stringified filter.
func (o *OneOfFilter) String() string {
	values := fmt.Sprintf(`["%s"]`, Join(`","`, 1, o.Values...))
	return fmt.Sprintf(`"%s": {"%s": %s}`, o.Field, o.Operator, values)
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
	return fmt.Sprintf(`"%s": {"%s": "%s"}`, s.Field, s.Operator, s.Value)
}

// StrFilter returns a new instance of StringFilter.
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
