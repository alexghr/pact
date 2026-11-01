package session

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/alexghr/pact/internal/approval"
	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/internal/hostbridge"
	"github.com/alexghr/pact/internal/mcpproxy"
)

func mcpServers(settings map[string]config.MCPServer, approvals *approval.Client) ([]hostbridge.Server, error) {
	names := make([]string, 0, len(settings))
	for name := range settings {
		names = append(names, name)
	}
	slices.Sort(names)
	servers := make([]hostbridge.Server, 0, len(names))
	for _, name := range names {
		proxy, err := mcpProxy(name, settings[name], approvals)
		if err != nil {
			return nil, fmt.Errorf("mcp.%s: %w", name, err)
		}
		servers = append(servers, hostbridge.Server{Path: "/mcp/" + name, Handler: proxy})
	}
	return servers, nil
}

func mcpProxy(name string, server config.MCPServer, approvals *approval.Client) (http.Handler, error) {
	if (server.Token == "") == (server.TokenFile == "") {
		return nil, errors.New("requires exactly one of token or tokenFile")
	}
	headers := func(context.Context) (http.Header, error) {
		token := server.Token
		if server.TokenFile != "" {
			data, err := os.ReadFile(server.TokenFile)
			if err != nil {
				return nil, errors.New("cannot read tokenFile")
			}
			token = string(data)
		}
		token = strings.TrimSpace(token)
		if token == "" || strings.ContainsAny(token, "\r\n") {
			return nil, errors.New("token must be nonempty and contain no line breaks")
		}
		return http.Header{"Authorization": {"Bearer " + token}}, nil
	}
	policy := mcpproxy.Policy{AllowedTools: server.AllowedTools, ApprovalTools: server.ApprovalTools}
	if approvals != nil {
		policy.Approve = func(ctx context.Context, tool string, arguments jsontext.Value, rule string) (bool, error) {
			return approvals.Approve(ctx, approval.ToolCall{Server: name, Tool: tool, Arguments: arguments, Rule: rule})
		}
	}
	proxy, err := mcpproxy.New(server.URL, headers, policy)
	if err != nil {
		return nil, err
	}
	if _, err := headers(context.Background()); err != nil {
		return nil, err
	}
	return proxy, nil
}
