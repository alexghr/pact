package session

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alexghr/pact/internal/config"
)

func TestMCPTokenRotationAndFailure(t *testing.T) {
	var authorizations []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		if r.Header.Get("Cookie") != "" {
			t.Error("client cookie reached upstream")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	writeToken := func(token string) {
		t.Helper()
		if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeToken("first\n")
	servers, err := mcpServers(map[string]config.MCPServer{"example": {
		URL: upstream.URL, TokenFile: tokenFile, AllowedTools: []string{"^get_issue$"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(servers[0].Handler)
	defer proxy.Close()
	request := func(want int) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, proxy.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_issue"}}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer client-token")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", "client=secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("status %d, want %d: %s", resp.StatusCode, want, body)
		}
	}
	request(http.StatusNoContent)
	writeToken("second\n")
	request(http.StatusNoContent)
	writeToken("\n")
	request(http.StatusBadGateway)
	if len(authorizations) != 2 || authorizations[0] != "Bearer first" || authorizations[1] != "Bearer second" {
		t.Fatalf("unexpected upstream authorizations: %v", authorizations)
	}
}

func TestMCPToolPermissions(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer host-token" {
			t.Error("missing host credentials")
		}
		if r.Header.Get("Mcp-Session-Id") != "session-1" {
			t.Error("session header was not preserved")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Mcp-Session-Id", "session-1")
		// Echo the body over SSE to check that inspection preserves its bytes.
		_, _ = io.Copy(w, r.Body)
	}))
	defer upstream.Close()

	call := func(name string) string {
		return `{"jsonrpc":"2.0","id":9007199254740993,"method":"tools/call","params":{"name":"` + name + `","arguments":{"text":"tools/call"}}}`
	}
	for _, tt := range []struct {
		name   string
		policy []string
		body   string
		method string
		status int
	}{
		{name: "omitted policy", body: call("get_issue"), status: http.StatusForbidden},
		{name: "empty policy", policy: []string{}, body: call("get_issue"), status: http.StatusForbidden},
		{name: "first pattern", policy: []string{"^get_.*$", "^list_.*$"}, body: call("get_issue"), status: http.StatusOK},
		{name: "second pattern", policy: []string{"^get_.*$", "^list_.*$"}, body: call("list_issues"), status: http.StatusOK},
		{name: "write denied", policy: []string{"^get_.*$", "^list_.*$"}, body: call("delete_issue"), status: http.StatusForbidden},
		{name: "case sensitive", policy: []string{"^get_.*$"}, body: call("GET_issue"), status: http.StatusForbidden},
		{name: "anchored name", policy: []string{"^get_issue$"}, body: call("get_issue_and_delete"), status: http.StatusForbidden},
		{name: "explicit allow all", policy: []string{".*"}, body: call("delete_issue"), status: http.StatusOK},
		{name: "initialize", body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, status: http.StatusOK},
		{name: "discovery", body: `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, status: http.StatusOK},
		{name: "notification", body: `{"jsonrpc":"2.0","method":"notifications/initialized"}`, status: http.StatusOK},
		{name: "client response", body: `{"jsonrpc":"2.0","id":"server-request","result":{"roots":[]}}`, status: http.StatusOK},
		{name: "event stream", method: http.MethodGet, status: http.StatusOK},
		{name: "session deletion", method: http.MethodDelete, status: http.StatusOK},
		{name: "oversized call", policy: []string{".*"}, body: `{"method":"tools/call","params":{"name":"get_issue","arguments":{"text":"` + strings.Repeat("x", 2<<20) + `"}}}`, status: http.StatusRequestEntityTooLarge},
		{name: "batch", policy: []string{"^get_.*$"}, body: `[` + call("get_issue") + `,` + call("delete_issue") + `]`, status: http.StatusBadRequest},
		{name: "trailing message", body: call("delete_issue") + call("get_issue"), status: http.StatusBadRequest},
		{name: "truncated message", body: `{"method":"tools/call"`, status: http.StatusBadRequest},
		{name: "missing name", policy: []string{".*"}, body: `{"method":"tools/call","params":{}}`, status: http.StatusBadRequest},
		{name: "non-string name", policy: []string{".*"}, body: `{"method":"tools/call","params":{"name":123}}`, status: http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			servers, err := mcpServers(map[string]config.MCPServer{"example": {
				URL: upstream.URL, Token: "host-token", AllowedTools: tt.policy,
			}})
			if err != nil {
				t.Fatal(err)
			}
			method := tt.method
			if method == "" {
				method = http.MethodPost
			}
			req := httptest.NewRequest(method, "/mcp/example", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Mcp-Session-Id", "session-1")
			response := httptest.NewRecorder()
			before := calls.Load()
			servers[0].Handler.ServeHTTP(response, req)
			if response.Code != tt.status {
				t.Fatalf("status %d, want %d: %s", response.Code, tt.status, response.Body)
			}
			if tt.status == http.StatusOK {
				if calls.Load() != before+1 || response.Body.String() != tt.body || response.Header().Get("Content-Type") != "text/event-stream" || response.Header().Get("Mcp-Session-Id") != "session-1" {
					t.Fatalf("MCP forwarding changed: %v %s", response.Header(), response.Body)
				}
			} else if calls.Load() != before {
				t.Fatal("rejected request reached upstream")
			}
			if tt.status == http.StatusForbidden {
				var result struct {
					ID    json.Number              `json:"id"`
					Error struct{ Message string } `json:"error"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.ID.String() != "9007199254740993" || !strings.Contains(result.Error.Message, "allowedTools") {
					t.Fatalf("missing tool rejection or request ID: %s (%v)", response.Body, err)
				}
			}
		})
	}
}

func TestMCPInvalidToolPolicy(t *testing.T) {
	_, err := mcpServers(map[string]config.MCPServer{"example": {
		URL: "http://127.0.0.1/mcp", Token: "host-token", AllowedTools: []string{"^get_", "["},
	}})
	if err == nil || !strings.Contains(err.Error(), "mcp.example: allowedTools[1]") {
		t.Fatalf("invalid policy did not fail with config context: %v", err)
	}
}
