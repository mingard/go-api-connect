// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Request stores request parameters.
type Request interface {
	// Initialize initializes the request.
	Initialize(proto, host, property, bearer string, port int) error

	// Do executes the HTTPRequest.
	Do(ctx context.Context) ([]byte, *http.Header, error)

	// MarshalBinary returns the request url as binary.
	MarshalBinary() ([]byte, error)
}

// fieldsToJSON converts a list of fields to JSON.
func fieldsToJSON(fields []string) string {
	f := make(map[string]int, 0)
	for _, field := range fields {
		f[field] = 1
	}
	if j, err := json.Marshal(f); err == nil {
		return string(j)
	}

	return ""
}

// errNoProtocol is the error of a URL built with no protocol.
var errNoProtocol = errors.New(`no protocol: want "https", or "http" on a trusted network`)

// buildURL returns proto://host:port followed by the path segments, each
// escaped as one segment by escapeSegment. host may be an IPv6 address, with
// or without brackets, and with a zone (see unbracket).
func buildURL(proto, host string, port int, segments ...string) (string, error) {
	if proto == "" {
		return "", errNoProtocol
	}
	escaped := make([]string, len(segments))
	for i, s := range segments {
		escaped[i] = escapeSegment(s)
	}
	raw := "/" + strings.Join(escaped, "/")
	// Path is the decoded form of raw, so that String keeps raw as it is,
	// with any escape the caller wrote, rather than encoding it again.
	path, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	u := url.URL{
		Scheme:  proto,
		Host:    net.JoinHostPort(unbracket(host), strconv.Itoa(port)),
		Path:    path,
		RawPath: raw,
	}
	return u.String(), nil
}

// requestURL returns the URL of a collection, or of something in it: the
// segments of property, which keeps its slashes ("1.0/library" is a version
// and a database), the segments of collection, which keeps its slashes too
// (in 1.1.0 a Collection of "books/<id>" was the way to reach one document),
// then the given segments, each one segment whatever it holds.
func requestURL(proto, host, property, collection string, port int, segments ...string) (string, error) {
	all := strings.Split(property, "/")
	all = append(all, strings.Split(collection, "/")...)
	u, err := buildURL(proto, host, port, append(all, segments...)...)
	if err != nil {
		return "", fmt.Errorf("apiconnect: %w", err)
	}
	return u, nil
}

// documentURL returns the URL of a collection, or of the document id in it
// when id is not empty.
func documentURL(proto, host, property string, port int, collection, id string) (string, error) {
	if id == "" {
		return requestURL(proto, host, property, collection, port)
	}
	return requestURL(proto, host, property, collection, port, id)
}

// escapeSegment escapes s as one path segment. A valid percent-escape, such
// as %2F, is kept as it is: 1.1.0 sent it unchanged, so a value its caller
// encoded is not encoded twice. Everything else that cannot stand in a
// segment is percent-encoded with url.PathEscape: "/", "?", "#", a space,
// and a "%" that starts no valid escape, as in "50%off". A "." or ".."
// segment is written %2E or %2E%2E, so that it cannot climb the path.
func escapeSegment(s string) string {
	if s == "." || s == ".." {
		return strings.Repeat("%2E", len(s))
	}
	var b strings.Builder
	start := 0
	for i := 0; i+2 < len(s); i++ {
		if s[i] == '%' && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteString(url.PathEscape(s[start:i]))
			b.WriteString(s[i : i+3])
			i += 2
			start = i + 1
		}
	}
	b.WriteString(url.PathEscape(s[start:]))
	return b.String()
}

// isHex reports whether c is a hexadecimal digit.
func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// unbracket removes the brackets around an IPv6 address, which
// net.JoinHostPort adds back. Inside brackets, a zone may be written in URL
// syntax (RFC 6874), [fe80::1%25en0], as v1.1.0 accepted: its "%25"
// separator, and any escape in the zone, are decoded here, so that the zone
// is escaped once, not twice. fe80::1%en0 and [fe80::1%en0] work too.
func unbracket(host string) string {
	if len(host) > 1 && host[0] == '[' && host[len(host)-1] == ']' {
		host = host[1 : len(host)-1]
		if addr, zone, ok := strings.Cut(host, "%25"); ok {
			if z, err := url.PathUnescape(zone); err == nil {
				zone = z
			}
			host = addr + "%" + zone
		}
	}
	return host
}

// marshalURL returns the URL of h's request as binary, or ErrNotInitialized.
func marshalURL(h *HTTPRequest) ([]byte, error) {
	if h == nil || h.Req == nil {
		return nil, ErrNotInitialized
	}
	return h.Req.URL.MarshalBinary()
}

// Get stores Get parameters.
//
// Get asks for a page of the collection, or, when ID is set, for the
// document with that ID. A "/" in Collection separates path segments, as in
// 1.1.0, so a Collection of "books/<id>" still reaches the document; ID is
// always one segment, so a "/" in it is escaped.
//
// Get embeds *Sort, which is nil until set: set it with NewSort before
// calling Asc or Desc through a Get.
type Get struct {
	*Sort
	Metadata     bool
	Compose      bool
	ComposeAll   bool
	SkipCache    bool
	ComposeLevel uint64
	Collection   string
	Page         uint64
	Limit        uint64
	Fields       []string
	Query        *Filters
	HTTPRequest  *HTTPRequest
	ETag         string

	// ID, when set, asks for the document with that ID: the path is the
	// collection's, then the ID, escaped as one segment. In 1.1.0 the way to
	// do this was a Collection of "books/<id>", which still works.
	ID string
}

// Initialize initializes the request. If-None-Match is sent only when ETag
// is set.
func (g *Get) Initialize(proto, host, property, bearer string, port int) error {
	g.HTTPRequest = &HTTPRequest{
		Method: http.MethodGet,
		Query:  make(map[string]string),
	}

	var segments []string
	if g.ID != "" {
		segments = append(segments, g.ID)
	}
	if g.Metadata {
		segments = append(segments, "count")
	}
	u, err := requestURL(proto, host, property, g.Collection, port, segments...)
	if err != nil {
		return err
	}
	g.HTTPRequest.URL = u

	if err := g.HTTPRequest.Initialize(); err != nil {
		return err
	}

	if g.ETag != "" {
		g.HTTPRequest.SetHeader("If-None-Match", g.ETag)
	}
	g.HTTPRequest.SetHeader("Content-Type", "application/json")
	g.HTTPRequest.SetParam("page", strconv.FormatUint(g.Page, 10))
	g.HTTPRequest.SetParam("count", strconv.FormatUint(g.Limit, 10))
	// Set auth header.
	g.HTTPRequest.SetHeader("Authorization", fmt.Sprintf("Bearer %s", bearer))

	if g.SkipCache {
		g.HTTPRequest.SetParam("cache", strconv.FormatBool(false))
	}

	if g.Sort != nil {
		g.HTTPRequest.SetParam("sort", g.Sort.String())
	}

	if g.Compose {
		g.HTTPRequest.SetParam("compose", strconv.FormatBool(g.Compose))
	}

	if g.ComposeLevel > 0 {
		g.HTTPRequest.SetParam("compose", strconv.FormatUint(g.ComposeLevel, 10))
	}

	if g.ComposeAll {
		g.HTTPRequest.SetParam("compose", "all")
	}

	if g.Query != nil {
		g.HTTPRequest.SetParam("filter", g.Query.ToQueryString())
	}

	if g.Fields != nil {
		g.HTTPRequest.SetParam("fields", fieldsToJSON(g.Fields))
	}

	return nil
}

// MarshalBinary returns the request url as binary, or ErrNotInitialized
// before Initialize has succeeded.
func (g *Get) MarshalBinary() ([]byte, error) {
	return marshalURL(g.HTTPRequest)
}

// Do executes the HTTPRequest.
func (g *Get) Do(ctx context.Context) ([]byte, *http.Header, error) {
	return g.HTTPRequest.Do(ctx)
}

func (g *Get) httpRequest() *HTTPRequest { return g.HTTPRequest }

// Post stores post data configurations.
type Post struct {
	ID          string
	Collection  string
	Body        []byte
	HTTPRequest *HTTPRequest
}

// Initialize initializes the request.
func (p *Post) Initialize(proto, host, property, bearer string, port int) error {
	p.HTTPRequest = &HTTPRequest{
		Method:  http.MethodPost,
		Payload: bytes.NewBuffer(p.Body),
		Query:   make(map[string]string),
	}

	u, err := documentURL(proto, host, property, port, p.Collection, p.ID)
	if err != nil {
		return err
	}
	p.HTTPRequest.URL = u

	if err := p.HTTPRequest.Initialize(); err != nil {
		return err
	}

	p.HTTPRequest.SetHeader("Content-Type", "application/json")
	// Set auth header.
	p.HTTPRequest.SetHeader("Authorization", fmt.Sprintf("Bearer %s", bearer))

	return nil
}

// MarshalBinary returns the request url as binary, or ErrNotInitialized
// before Initialize has succeeded.
func (p *Post) MarshalBinary() ([]byte, error) {
	return marshalURL(p.HTTPRequest)
}

// Do executes the HTTPRequest.
func (p *Post) Do(ctx context.Context) ([]byte, *http.Header, error) {
	return p.HTTPRequest.Do(ctx)
}

func (p *Post) httpRequest() *HTTPRequest { return p.HTTPRequest }

// Put stores put data.
type Put struct {
	ID          string
	Collection  string
	Body        []byte
	HTTPRequest *HTTPRequest
}

// Initialize initializes the request.
func (p *Put) Initialize(proto, host, property, bearer string, port int) error {
	p.HTTPRequest = &HTTPRequest{
		Method:  http.MethodPut,
		Payload: bytes.NewBuffer(p.Body),
		Query:   make(map[string]string),
	}

	u, err := documentURL(proto, host, property, port, p.Collection, p.ID)
	if err != nil {
		return err
	}
	p.HTTPRequest.URL = u

	if err := p.HTTPRequest.Initialize(); err != nil {
		return err
	}

	p.HTTPRequest.SetHeader("Content-Type", "application/json")
	// Set auth header.
	p.HTTPRequest.SetHeader("Authorization", fmt.Sprintf("Bearer %s", bearer))

	return nil
}

// MarshalBinary returns the request url as binary, or ErrNotInitialized
// before Initialize has succeeded.
func (p *Put) MarshalBinary() ([]byte, error) {
	return marshalURL(p.HTTPRequest)
}

// Do executes the HTTPRequest.
func (p *Put) Do(ctx context.Context) ([]byte, *http.Header, error) {
	return p.HTTPRequest.Do(ctx)
}

func (p *Put) httpRequest() *HTTPRequest { return p.HTTPRequest }

// Delete stores delete data.
type Delete struct {
	ID          string
	Collection  string
	HTTPRequest *HTTPRequest
}

// Initialize initializes the request. An empty ID is an error: the request
// would otherwise go to the collection itself.
func (d *Delete) Initialize(proto, host, property, bearer string, port int) error {
	d.HTTPRequest = &HTTPRequest{
		Method: http.MethodDelete,
		Query:  make(map[string]string),
	}

	if d.ID == "" {
		return errors.New("apiconnect: a Delete needs an ID")
	}
	u, err := requestURL(proto, host, property, d.Collection, port, d.ID)
	if err != nil {
		return err
	}
	d.HTTPRequest.URL = u

	if err := d.HTTPRequest.Initialize(); err != nil {
		return err
	}

	d.HTTPRequest.SetHeader("Content-Type", "application/json")
	// Set auth header.
	d.HTTPRequest.SetHeader("Authorization", fmt.Sprintf("Bearer %s", bearer))

	return nil
}

// MarshalBinary returns the request url as binary, or ErrNotInitialized
// before Initialize has succeeded.
func (d *Delete) MarshalBinary() ([]byte, error) {
	return marshalURL(d.HTTPRequest)
}

// Do executes the HTTPRequest.
func (d *Delete) Do(ctx context.Context) ([]byte, *http.Header, error) {
	return d.HTTPRequest.Do(ctx)
}

func (d *Delete) httpRequest() *HTTPRequest { return d.HTTPRequest }

// Sort is a wrapper for field sorting.
//
// String lists the fields added with Asc and Desc in the order of the
// calls, so that the first field takes precedence, and then any fields
// written straight into Value, in sorted order.
//
// A Sort records the order of the calls in an unexported field, so a Sort
// built with Asc or Desc is not reflect.DeepEqual to one whose Value was
// written directly. To compare what two Sorts send, compare their String.
type Sort struct {
	Value map[string]int8
	order []string // fields added with Asc and Desc, first call first
}

// Asc adds an ascending sort field. A field added again keeps its place and
// takes the new direction.
func (s *Sort) Asc(field string) *Sort {
	return s.add(field, 1)
}

// Desc adds a descending sort field. A field added again keeps its place and
// takes the new direction.
func (s *Sort) Desc(field string) *Sort {
	return s.add(field, -1)
}

func (s *Sort) add(field string, direction int8) *Sort {
	if s.Value == nil {
		s.Value = make(map[string]int8)
	}
	if !slices.Contains(s.order, field) {
		s.order = append(s.order, field)
	}
	s.Value[field] = direction
	return s
}

// String returns a stringified sort list, such as
// {"publishedAt": -1,"title": 1}. A nil Sort is {}.
func (s *Sort) String() string {
	if s == nil {
		return "{}"
	}
	vals := make([]string, 0, len(s.Value))
	listed := make(map[string]bool, len(s.Value))
	for _, k := range s.order {
		if v, ok := s.Value[k]; ok && !listed[k] {
			listed[k] = true
			vals = append(vals, sortField(k, v))
		}
	}
	var rest []string
	for k := range s.Value {
		if !listed[k] {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	for _, k := range rest {
		vals = append(vals, sortField(k, s.Value[k]))
	}
	return fmt.Sprintf(`{%s}`, Join(",", 1, vals...))
}

// sortField returns `"field": direction`, the field as a JSON string.
func sortField(field string, direction int8) string {
	return jsonString(field) + ": " + strconv.Itoa(int(direction))
}

// NewSort returns a new instance of Sort.
func NewSort() *Sort {
	return &Sort{
		Value: make(map[string]int8),
	}
}
