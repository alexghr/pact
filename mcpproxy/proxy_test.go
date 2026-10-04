package mcpproxy_test

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

	"github.com/alexghr/pact/mcpproxy"
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
		if r.Header.Get("Cookie") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-Host") != "" {
			t.Error("client credentials or forwarding headers reached upstream")
		}
		if r.Host != r.Context().Value(http.LocalAddrContextKey).(net.Addr).String() {
			t.Error("client controlled the upstream Host header")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"anything"},"id":1}` {
			t.Errorf("MCP payload changed: %q, %v", body, err)
		}
		w.Header().Set("Mcp-Session-Id", "upstream-session")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer upstream.Close()

	var credentials atomic.Int32
	proxy, err := mcpproxy.NewHTTP(upstream.URL+"/private/mcp?tenant=host", func(context.Context) (http.Header, error) {
		return http.Header{
			"Authorization": {fmt.Sprintf("Bearer host-%d", credentials.Add(1))},
			"X-Api-Key":     {"host-key"},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		r := httptest.NewRequest(http.MethodPost, "http://attacker.invalid/admin?tenant=attacker", strings.NewReader(
			`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"anything"},"id":1}`,
		))
		r.Header.Set("Authorization", "Bearer attacker")
		r.Header.Set("Cookie", "session=attacker")
		r.Header.Set("X-Api-Key", "attacker-key")
		r.Header.Set("Forwarded", "host=attacker.invalid")
		r.Header.Set("X-Forwarded-Host", "attacker.invalid")
		// Hop-by-hop stripping must happen before host credentials are added.
		r.Header.Set("Connection", "Authorization, X-Api-Key")
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Header().Get("Mcp-Session-Id") != "upstream-session" {
			t.Fatalf("proxy response: %d %s", w.Code, w.Body)
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
	proxy, err := mcpproxy.NewHTTP(upstream.URL, func(context.Context) (http.Header, error) {
		return nil, errors.New("credential provider failed: secret-value")
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "secret-value") || calls.Load() != 0 {
		t.Fatalf("credential failure did not fail closed: %d %s (upstream calls %d)", w.Code, w.Body, calls.Load())
	}

	upstream.Close()
	proxy, err = mcpproxy.NewHTTP(upstream.URL+"?secret=secret-value", nil)
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	proxy.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "secret-value") {
		t.Fatalf("transport failure exposed endpoint details: %d %s", w.Code, w.Body)
	}
	if strings.Contains(logs.String(), "secret-value") {
		t.Fatal("proxy errors exposed credentials in logs")
	}
}
