package httpproxy_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alexghr/pact/httpproxy"
)

func TestHostCredentialsAndDestination(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if r.URL.String() != "/private/mcp?tenant=host" {
			t.Errorf("client changed destination: %s", r.URL)
		}
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer host-%d", call) || r.Header.Get("X-Api-Key") != "host-key" {
			t.Error("host credentials were overridden or removed")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("client credentials reached upstream")
		}
		if r.Host != r.Context().Value(http.LocalAddrContextKey).(net.Addr).String() {
			t.Error("client controlled the upstream Host header")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	var credentials atomic.Int32
	proxy, err := httpproxy.NewHTTP(upstream.URL+"/private/mcp?tenant=host", func(context.Context) (http.Header, error) {
		return http.Header{
			"Authorization": {fmt.Sprintf("Bearer host-%d", credentials.Add(1))},
			"X-Api-Key":     {"host-key"},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	frontend := httptest.NewServer(proxy)
	defer frontend.Close()
	for range 2 {
		r, err := http.NewRequest(http.MethodPost, frontend.URL+"/admin?tenant=attacker", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Host = "attacker.invalid"
		r.Header.Set("Authorization", "Bearer attacker")
		r.Header.Set("Proxy-Authorization", "Bearer attacker-proxy")
		r.Header.Set("Cookie", "session=attacker")
		r.Header.Set("X-Api-Key", "attacker-key")
		// Hop-by-hop stripping must happen before host credentials are added.
		r.Header.Set("Connection", "Authorization, X-Api-Key")
		res, err := frontend.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("proxy response: %s", res.Status)
		}
	}
	if calls.Load() != 2 || credentials.Load() != 2 {
		t.Fatal("credentials were not refreshed for each request")
	}
}

func TestProxyErrorsDoNotExposeCredentials(t *testing.T) {
	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousOutput)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	checkError := func(endpoint string, headers httpproxy.HeadersFunc) {
		t.Helper()
		proxy, err := httpproxy.NewHTTP(endpoint, headers)
		if err != nil {
			t.Fatal(err)
		}
		frontend := httptest.NewServer(proxy)
		defer frontend.Close()
		res, err := frontend.Client().Get(frontend.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusBadGateway || strings.Contains(string(body), "secret-value") || strings.Contains(logs.String(), "secret-value") {
			t.Fatalf("proxy failure exposed credentials: %s %s (logs %s)", res.Status, body, &logs)
		}
	}
	checkError(upstream.URL, func(context.Context) (http.Header, error) {
		return nil, errors.New("credential provider failed: secret-value")
	})
	if calls.Load() != 0 {
		t.Fatal("credential failure reached upstream")
	}
	upstream.Close()
	checkError(upstream.URL+"?secret=secret-value", nil)
}
