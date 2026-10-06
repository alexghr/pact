package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/mcpproxy"
)

func mcpServers(settings map[string]config.MCPServer) ([]mcpproxy.Server, error) {
	names := make([]string, 0, len(settings))
	for name := range settings {
		names = append(names, name)
	}
	slices.Sort(names)
	servers := make([]mcpproxy.Server, 0, len(names))
	for _, name := range names {
		proxy, err := mcpProxy(settings[name])
		if err != nil {
			return nil, fmt.Errorf("mcp.%s: %w", name, err)
		}
		servers = append(servers, mcpproxy.Server{Name: name, Handler: proxy})
	}
	return servers, nil
}

func mcpProxy(server config.MCPServer) (http.Handler, error) {
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
	proxy, err := mcpproxy.NewHTTP(server.URL, headers)
	if err != nil {
		return nil, fmt.Errorf("url: %w", err)
	}
	if _, err := headers(context.Background()); err != nil {
		return nil, err
	}
	return proxy, nil
}
