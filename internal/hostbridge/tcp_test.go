package hostbridge

import (
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestTCPAuthenticationAndLifetime(t *testing.T) {
	var calls atomic.Int32
	// Matching names in separate namespaces must reach distinct handlers, both
	// protected by the bridge capability URL.
	endpoints := []struct {
		path   string
		status int
	}{{"/mcp/example", http.StatusNoContent}, {"/harness/example", http.StatusAccepted}}
	var servers []Server
	for _, endpoint := range endpoints {
		servers = append(servers, Server{Path: endpoint.path, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.URL.Path != endpoint.path {
				t.Error("bridge token reached handler or request path changed")
			}
			w.WriteHeader(endpoint.status)
		})})
	}
	bridge, err := startTCP(servers)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	base, err := url.Parse(bridge.hostURL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := bridge.hostURL + "/mcp/example"
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	// Prefix lookalikes must not grant access or trigger token-bearing redirects.
	for _, endpoint := range endpoints {
		for _, path := range []string{"", "/wrong", base.Path + "extra", base.Path} {
			req, err := http.NewRequest(http.MethodPost, base.Scheme+"://"+base.Host+path+endpoint.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			want := http.StatusUnauthorized
			if path == base.Path {
				want = endpoint.status
			}
			if resp.StatusCode != want {
				t.Fatalf("%s: got %d, want %d", endpoint.path, resp.StatusCode, want)
			}
		}
	}
	if calls.Load() != int32(len(endpoints)) {
		t.Fatalf("handler called %d times", calls.Load())
	}
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(endpoint); err == nil {
		t.Fatal("bridge accepted requests after shutdown")
	}
}
