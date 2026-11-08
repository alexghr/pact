package session

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/approval"
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
	}}, nil)
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
			}}, nil)
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
	for _, tt := range []struct {
		allowed, approval []string
		error             string
	}{
		{allowed: []string{"^get_", "["}, error: "allowedTools[1]"},
		{approval: []string{"["}, error: "approvalTools[0]"},
		{approval: []string{".*"}, error: "approvalTools requires approvals.socket"},
	} {
		_, err := mcpServers(map[string]config.MCPServer{"example": {
			URL: "http://127.0.0.1/mcp", Token: "host-token", AllowedTools: tt.allowed, ApprovalTools: tt.approval,
		}}, nil)
		if err == nil || !strings.Contains(err.Error(), "mcp.example: "+tt.error) {
			t.Fatalf("invalid policy did not fail with config context: %v", err)
		}
	}
}

func TestMCPApprovals(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	type reply struct {
		status int
		body   string
	}
	type pending struct {
		request approval.Request
		reply   chan reply
		closed  chan struct{}
	}
	requests := make(chan pending, 1)
	socket := filepath.Join(t.TempDir(), "approval.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	service := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/approvals" {
			t.Errorf("invalid approval request: %s %s", r.Method, r.URL)
		}
		data, err := io.ReadAll(r.Body)
		var request approval.Request
		if err != nil || json.Unmarshal(data, &request) != nil || strings.Contains(string(data), "host-secret") {
			t.Error("invalid approval payload or exposed credentials")
			return
		}
		p := pending{request: request, reply: make(chan reply, 1), closed: make(chan struct{})}
		defer close(p.closed)
		select {
		case requests <- p:
		case <-r.Context().Done():
			return
		}
		select {
		case response := <-p.reply:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Location", "/v1/approvals")
			w.WriteHeader(response.status)
			_, _ = io.WriteString(w, response.body)
		case <-r.Context().Done():
		}
	})}
	serviceDone := make(chan struct{})
	go func() { defer close(serviceDone); _ = service.Serve(listener) }()
	t.Cleanup(func() { _ = service.Close(); <-serviceDone })

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer host-secret" {
			t.Error("missing upstream credentials")
		}
		_, _ = io.Copy(w, r.Body)
	}))
	defer upstream.Close()
	settings := config.Approvals{Socket: socket, TimeoutSeconds: 1}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	session := approval.Session{ID: 42, Key: "session-key", ExecutionID: 17, Title: "Fix deployment", Workspace: t.TempDir(), Backend: "codex"}
	newProxy := func(settings config.Approvals) http.Handler {
		t.Helper()
		client, err := approval.New(settings, session)
		if err != nil {
			t.Fatal(err)
		}
		servers, err := mcpServers(map[string]config.MCPServer{"forgejo": {
			URL: upstream.URL, Token: "host-secret", AllowedTools: []string{"^get_", "^create_overlap$"}, ApprovalTools: []string{"^create_"},
		}}, client)
		if err != nil {
			t.Fatal(err)
		}
		return servers[0].Handler
	}
	proxy := newProxy(settings)
	call := func(tool string) string {
		return `{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"` + tool + `","arguments":{"owner":"team","repo":"project","title":"Investigate <deployment>"}}}`
	}
	invoke := func(ctx context.Context, handler http.Handler, body string) <-chan *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp/forgejo", strings.NewReader(body)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		response := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			response <- w
		}()
		return response
	}
	awaitResponse := func(response <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		t.Helper()
		select {
		case w := <-response:
			return w
		case <-ctx.Done():
			t.Fatal("MCP call did not finish")
			return nil
		}
	}
	seen := make(map[string]bool)
	for _, tt := range []struct {
		name, tool, decision string
		status               int
	}{
		{"approve", "create_issue", "approve", http.StatusOK},
		{"ask again", "create_issue", "approve", http.StatusOK},
		{"approval overrides allow", "create_overlap", "reject", http.StatusForbidden},
		{"reject", "create_issue", "reject", http.StatusForbidden},
		{"unknown decision", "create_issue", "always", http.StatusServiceUnavailable},
		{"stale ID", "create_issue", "stale", http.StatusServiceUnavailable},
		{"invalid reply", "create_issue", "invalid", http.StatusServiceUnavailable},
		{"redirect", "create_issue", "redirect", http.StatusServiceUnavailable},
		{"cancel", "create_issue", "cancel", http.StatusServiceUnavailable},
		{"timeout", "create_issue", "timeout", http.StatusServiceUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requestCtx, stop := context.WithCancel(ctx)
			defer stop()
			before := upstreamCalls.Load()
			body := call(tt.tool)
			response := invoke(requestCtx, proxy, body)
			var p pending
			select {
			case p = <-requests:
			case <-ctx.Done():
				t.Fatal("missing approval request")
			}
			r := p.request
			if upstreamCalls.Load() != before {
				t.Fatal("call reached upstream before approval")
			}
			if r.ID == "" || seen[r.ID] || r.Hostname != hostname || r.Session != session || r.Call.Server != "forgejo" || r.Call.Tool != tt.tool || r.Call.Rule != "^create_" || string(r.Call.Arguments) != `{"owner":"team","repo":"project","title":"Investigate <deployment>"}` || !r.ExpiresAt.After(r.CreatedAt) {
				t.Fatalf("approval lacks call context or has a reused ID: %+v", r)
			}
			seen[r.ID] = true
			decision := approval.Response{ID: r.ID, Decision: tt.decision}
			status := http.StatusOK
			switch tt.decision {
			case "stale":
				decision.ID = "previous-request"
				decision.Decision = "approve"
			case "redirect":
				status = http.StatusTemporaryRedirect
			case "cancel":
				stop()
			}
			if tt.decision != "timeout" && tt.decision != "cancel" {
				data, _ := json.Marshal(decision)
				if tt.decision == "invalid" {
					data = []byte(`{"decision":`)
				}
				p.reply <- reply{status: status, body: string(data)}
			}
			w := awaitResponse(response)
			if w.Code != tt.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.status, w.Body)
			}
			if tt.status == http.StatusOK {
				if upstreamCalls.Load() != before+1 || w.Body.String() != body {
					t.Fatal("approved request was changed or not forwarded exactly once")
				}
			} else if upstreamCalls.Load() != before {
				t.Fatal("unapproved call reached upstream")
			}
			select {
			case <-p.closed:
			case <-ctx.Done():
				t.Fatal("approval connection survived cancellation or timeout")
			}
		})
	}
	t.Run("case-sensitive arguments", func(t *testing.T) {
		before := upstreamCalls.Load()
		response := invoke(ctx, proxy, `{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"create_issue","arguments":{"repo":"production"},"Arguments":{"repo":"sandbox"}}}`)
		var p pending
		select {
		case p = <-requests:
		case <-ctx.Done():
			t.Fatal("missing approval request")
		}
		if got := string(p.request.Call.Arguments); got != `{"repo":"production"}` {
			t.Errorf("approval inspected different arguments from upstream: %s", got)
		}
		data, _ := json.Marshal(approval.Response{ID: p.request.ID, Decision: "reject"})
		p.reply <- reply{status: http.StatusOK, body: string(data)}
		if w := awaitResponse(response); w.Code != http.StatusForbidden || upstreamCalls.Load() != before {
			t.Fatal("rejected call reached upstream")
		}
	})
	for _, tt := range []struct{ name, params string }{
		{"duplicate parameters", `"params":{"name":"create_issue"},"params":{"name":"create_issue","arguments":{}}`},
		{"duplicate arguments", `"params":{"name":"create_issue","arguments":{"repo":"production"},"arguments":{"repo":"sandbox"}}`},
		{"duplicate argument member", `"params":{"name":"create_issue","arguments":{"repo":"production","repo":"sandbox"}}`},
		{"invalid UTF-8 argument", `"params":{"name":"create_issue","arguments":{"repo":"` + "\xff" + `"}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := upstreamCalls.Load()
			response := invoke(ctx, proxy, `{"jsonrpc":"2.0","id":99,"method":"tools/call",`+tt.params+`}`)
			if w := awaitResponse(response); w.Code != http.StatusBadRequest || upstreamCalls.Load() != before {
				t.Fatalf("ambiguous call was not rejected: %d %s", w.Code, w.Body)
			}
			select {
			case <-requests:
				t.Fatal("ambiguous call requested approval")
			default:
			}
		})
	}
	for tool, want := range map[string]int{"get_issue": http.StatusOK, "delete_issue": http.StatusForbidden} {
		response := invoke(ctx, proxy, call(tool))
		if w := awaitResponse(response); w.Code != want {
			t.Fatalf("tool %s: %d, want %d", tool, w.Code, want)
		}
		select {
		case <-requests:
			t.Fatal("automatic or denied call requested approval")
		default:
		}
	}
	settings.Socket = filepath.Join(t.TempDir(), "missing.sock")
	before := upstreamCalls.Load()
	response := invoke(ctx, newProxy(settings), call("create_issue"))
	if w := awaitResponse(response); w.Code != http.StatusServiceUnavailable || upstreamCalls.Load() != before {
		t.Fatal("unavailable approval service allowed a call")
	}
}
