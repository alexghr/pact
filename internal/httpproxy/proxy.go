// Package httpproxy forwards HTTP requests using credentials held by the host.
// It passes request and response bodies through without interpreting them.
package httpproxy

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// HeadersFunc supplies host-owned headers for each upstream request. It may be
// called concurrently and must return headers that remain unchanged while the
// request is being prepared. A failure prevents the request from reaching the
// upstream; its error text is never returned to the client or logged.
type HeadersFunc func(context.Context) (http.Header, error)

// NewHTTP forwards to the exact endpoint, including its path and query. Incoming
// URLs cannot change that destination. Credentials belong in headers, not URL
// userinfo. A nil headers callback is valid for an unauthenticated upstream.
//
// Client authorization and cookies are removed before host headers are applied.
// Other end-to-end headers and bodies pass through, including streaming replies.
// The upstream is trusted not to reflect credentials in its responses; response
// payloads are not inspected or redacted.
func NewHTTP(endpoint string, headers HeadersFunc) (http.Handler, error) {
	target, err := url.Parse(endpoint)
	if err != nil || target.Hostname() == "" ||
		(target.Scheme != "http" && target.Scheme != "https") ||
		target.User != nil || target.Fragment != "" || target.Opaque != "" {
		return nil, errors.New("proxy endpoint must be an absolute HTTP(S) URL without userinfo or a fragment")
	}

	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			endpoint := *target
			r.Out.URL = &endpoint
			r.Out.Host = target.Host
			r.Out.Header.Del("Authorization")
			r.Out.Header.Del("Proxy-Authorization")
			r.Out.Header.Del("Cookie")
		},
		Transport:     &credentialTransport{headers: headers},
		FlushInterval: -1,
		// Transport errors can contain credential-provider errors or URL queries.
		ErrorLog: log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "HTTP upstream unavailable", http.StatusBadGateway)
		},
	}, nil
}

type credentialTransport struct {
	headers HeadersFunc
}

func (t *credentialTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.headers != nil {
		headers, err := t.headers(r.Context())
		if err != nil {
			if r.Body != nil {
				_ = r.Body.Close()
			}
			return nil, err
		}
		r = r.Clone(r.Context())
		for name, values := range headers {
			r.Header.Del(name)
			for _, value := range values {
				r.Header.Add(name, value)
			}
		}
	}
	return http.DefaultTransport.RoundTrip(r)
}
