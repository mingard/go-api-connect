// Arthur Mingard
// (c) 2022 Arthur Mingard

package apiconnect

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"
)

// HTTPRequest stores http request data.
type HTTPRequest struct {
	Method  string
	URL     string
	Payload io.Reader
	Query   map[string]string
	Req     *http.Request
	Params  url.Values
}

// Do executes a request.
func (h *HTTPRequest) Do(ctx context.Context) ([]byte, *http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second*requestTimeout)
	defer cancel()

	if h.Params != nil && len(h.Params) > 0 {
		h.Req.URL.RawQuery = h.Params.Encode()
	}

	resp, err := http.DefaultClient.Do(h.Req.WithContext(ctx))

	if err != nil {
		return nil, nil, err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotModified {
		return nil, nil, fmt.Errorf("API request failed with code %d", resp.StatusCode)
	}

	defer resp.Body.Close()

	// No need for a body with a 304
	if resp.StatusCode == http.StatusNotModified {
		return nil, &resp.Header, err
	}

	body, err := ioutil.ReadAll(resp.Body)

	return body, &resp.Header, err
}

// SetParam sets URL parameters.
func (h *HTTPRequest) SetParam(k, v string) {
	h.Params.Set(k, v)
}

// SetHeader sets a header parameter.
func (h *HTTPRequest) SetHeader(k, v string) {
	h.Req.Header.Set(k, v)
}

// Initialize prepares the HTTP request..
func (h *HTTPRequest) Initialize() error {
	req, err := http.NewRequest(h.Method, h.URL, h.Payload)

	if err == nil {
		h.Req = req
	}

	h.Params = h.Req.URL.Query()

	return err
}
