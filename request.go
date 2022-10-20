// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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

// buildUrl builds the request url
func buildUrl(proto, host, property, collection string, port int) string {
	return fmt.Sprintf("%s://%s:%d/%s/%s", proto, host, port, property, collection)
}

// Get stores Get parameters.
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
}

// Initialize initializes the request.
func (g *Get) Initialize(proto, host, property, bearer string, port int) error {
	g.HTTPRequest = &HTTPRequest{
		Method: http.MethodGet,
		URL:    buildUrl(proto, host, property, g.Collection, port),
		Query:  make(map[string]string),
	}

	if g.Metadata {
		g.HTTPRequest.URL = fmt.Sprintf("%s/count", g.HTTPRequest.URL)
	}

	if err := g.HTTPRequest.Initialize(); err != nil {
		return err
	}

	g.HTTPRequest.SetHeader("If-None-Match", g.ETag)
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

// MarshalBinary returns the request url as binary.
func (g *Get) MarshalBinary() ([]byte, error) {
	return g.HTTPRequest.Req.URL.MarshalBinary()
}

// Do executes the HTTPRequest.
func (g *Get) Do(ctx context.Context) ([]byte, *http.Header, error) {
	return g.HTTPRequest.Do(ctx)
}

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
		URL:     buildUrl(proto, host, property, p.Collection, port),
		Payload: bytes.NewBuffer(p.Body),
		Query:   make(map[string]string),
	}

	if p.ID != "" {
		p.HTTPRequest.URL += fmt.Sprintf("/%s", p.ID)
	}

	if err := p.HTTPRequest.Initialize(); err != nil {
		return err
	}

	p.HTTPRequest.SetHeader("Content-Type", "application/json")
	// Set auth header.
	p.HTTPRequest.SetHeader("Authorization", fmt.Sprintf("Bearer %s", bearer))

	return nil
}

// MarshalBinary returns the request url as binary.
func (p *Post) MarshalBinary() ([]byte, error) {
	return p.HTTPRequest.Req.URL.MarshalBinary()
}

// Do executes the HTTPRequest.
func (p *Post) Do(ctx context.Context) ([]byte, *http.Header, error) {
	return p.HTTPRequest.Do(ctx)
}

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
		URL:     buildUrl(proto, host, property, p.Collection, port),
		Payload: bytes.NewBuffer(p.Body),
		Query:   make(map[string]string),
	}

	if p.ID != "" {
		p.HTTPRequest.URL += fmt.Sprintf("/%s", p.ID)
	}

	if err := p.HTTPRequest.Initialize(); err != nil {
		return err
	}

	p.HTTPRequest.SetHeader("Content-Type", "application/json")
	// Set auth header.
	p.HTTPRequest.SetHeader("Authorization", fmt.Sprintf("Bearer %s", bearer))

	return nil
}

// MarshalBinary returns the request url as binary.
func (p *Put) MarshalBinary() ([]byte, error) {
	return p.HTTPRequest.Req.URL.MarshalBinary()
}

// Do executes the HTTPRequest.
func (p *Put) Do(ctx context.Context) ([]byte, *http.Header, error) {
	return p.HTTPRequest.Do(ctx)
}

// Delete stores delete data.
type Delete struct {
	ID          string
	Collection  string
	HTTPRequest *HTTPRequest
}

// Initialize initializes the request.
func (d *Delete) Initialize(proto, host, property, bearer string, port int) error {
	d.HTTPRequest = &HTTPRequest{
		Method: http.MethodDelete,
		URL:    fmt.Sprintf("%s://%s:%d/%s/%s/%s", proto, host, port, property, d.Collection, d.ID),
		Query:  make(map[string]string),
	}

	if err := d.HTTPRequest.Initialize(); err != nil {
		return err
	}

	d.HTTPRequest.SetHeader("Content-Type", "application/json")
	// Set auth header.
	d.HTTPRequest.SetHeader("Authorization", fmt.Sprintf("Bearer %s", bearer))

	return nil
}

// MarshalBinary returns the request url as binary.
func (d *Delete) MarshalBinary() ([]byte, error) {
	return d.HTTPRequest.Req.URL.MarshalBinary()
}

// Do executes the HTTPRequest.
func (d *Delete) Do(ctx context.Context) ([]byte, *http.Header, error) {
	return d.HTTPRequest.Do(ctx)
}

// Sort is a wrapper for field sorting.
type Sort struct {
	Value map[string]int8
}

// Asc adds an ascending sort field.
func (s *Sort) Asc(field string) *Sort {
	s.Value[field] = 1
	return s
}

// Desc adds a descending sort field.
func (s *Sort) Desc(field string) *Sort {
	s.Value[field] = -1
	return s
}

// String returns a stringified sort list.
func (s *Sort) String() string {
	vals := make([]string, 0)
	for k, v := range s.Value {
		vals = append(vals, fmt.Sprintf(`"%s": %b`, k, v))
	}
	return fmt.Sprintf(`{%s}`, Join(",", 1, vals...))
}

// NewSort returns a new instance of Sort.
func NewSort() *Sort {
	return &Sort{
		Value: make(map[string]int8),
	}
}
