// Package mcpproxy enforces host-configured tool permissions before forwarding MCP.
package mcpproxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"

	"github.com/alexghr/pact/internal/httpproxy"
)

// New forwards Streamable HTTP MCP with a deny-by-default tool allow-list.
// Patterns use Go regexp syntax and match against the upstream tool name.
// Non-tool messages and streaming responses pass through without filtering.
func New(endpoint string, headers httpproxy.HeadersFunc, allowedTools []string) (http.Handler, error) {
	patterns := make([]*regexp.Regexp, 0, len(allowedTools))
	for i, pattern := range allowedTools {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("allowedTools[%d]: %w", i, err)
		}
		patterns = append(patterns, re)
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
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name string `json:"name"`
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
			allowed := false
			for _, pattern := range patterns {
				if pattern.MatchString(name) {
					allowed = true
					break
				}
			}
			if !allowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": message.ID,
					"error": map[string]any{"code": -32000, "message": fmt.Sprintf("Pact denied tool %q: not in allowedTools", name)},
				})
				return
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		proxy.ServeHTTP(w, r)
	}), nil
}
