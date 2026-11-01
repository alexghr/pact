// Package mcpproxy enforces host-configured tool permissions before forwarding MCP.
package mcpproxy

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"

	"github.com/alexghr/pact/internal/httpproxy"
)

type Policy struct {
	AllowedTools  []string
	ApprovalTools []string
	Approve       func(ctx context.Context, tool string, arguments jsontext.Value, rule string) (bool, error)
}

// New forwards Streamable HTTP MCP with a deny-by-default tool allow-list.
// Patterns use Go regexp syntax and match against the upstream tool name.
// Non-tool messages and streaming responses pass through without filtering.
func New(endpoint string, headers httpproxy.HeadersFunc, policy Policy) (http.Handler, error) {
	allowed, err := compilePatterns("allowedTools", policy.AllowedTools)
	if err != nil {
		return nil, err
	}
	approval, err := compilePatterns("approvalTools", policy.ApprovalTools)
	if err != nil {
		return nil, err
	}
	if len(approval) != 0 && policy.Approve == nil {
		return nil, errors.New("approvalTools requires approvals.socket")
	}
	proxy, err := httpproxy.NewHTTP(endpoint, headers)
	if err != nil {
		return nil, fmt.Errorf("url: %w", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// MCP sends messages via POST; other methods manage streams and sessions.
		if r.Method != http.MethodPost {
			proxy.ServeHTTP(w, r)
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		encoding := r.Header.Get("Content-Encoding")
		if err != nil || mediaType != "application/json" || (encoding != "" && encoding != "identity") {
			http.Error(w, "MCP messages require uncompressed application/json", http.StatusUnsupportedMediaType)
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		_ = r.Body.Close()
		if err != nil {
			status := http.StatusBadRequest
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				status = http.StatusRequestEntityTooLarge
			}
			http.Error(w, "cannot read MCP message (maximum 1 MiB)", status)
			return
		}
		var message struct {
			ID     jsontext.Value `json:"id"`
			Method string         `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments jsontext.Value `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal(data, &message); err != nil {
			http.Error(w, "expected a single MCP message object", http.StatusBadRequest)
			return
		}
		if message.Method == "tools/call" {
			name := message.Params.Name
			if name == "" {
				http.Error(w, "tools/call requires a tool name", http.StatusBadRequest)
				return
			}
			var rule *regexp.Regexp
			for _, pattern := range approval {
				if pattern.MatchString(name) {
					rule = pattern
					break
				}
			}
			permitted := false
			for _, pattern := range allowed {
				if pattern.MatchString(name) {
					permitted = true
					break
				}
			}
			if rule != nil {
				arguments := message.Params.Arguments
				if len(arguments) == 0 {
					arguments = jsontext.Value(`{}`)
				}
				var err error
				permitted, err = policy.Approve(r.Context(), name, arguments, rule.String())
				if err != nil {
					rpcError(w, message.ID, http.StatusServiceUnavailable, "Pact approval service unavailable")
					return
				}
				if !permitted {
					rpcError(w, message.ID, http.StatusForbidden, fmt.Sprintf("Pact approval rejected for tool %q", name))
					return
				}
			}
			if !permitted {
				rpcError(w, message.ID, http.StatusForbidden, fmt.Sprintf("Pact denied tool %q: not in allowedTools or approvalTools", name))
				return
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		proxy.ServeHTTP(w, r)
	}), nil
}

func compilePatterns(field string, patterns []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for i, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", field, i, err)
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}

func rpcError(w http.ResponseWriter, id jsontext.Value, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": -32000, "message": message},
	})
}
