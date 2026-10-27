package session

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	servers, err := mcpServers(map[string]config.MCPServer{"example": {URL: upstream.URL, TokenFile: tokenFile}})
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(servers[0].Handler)
	defer proxy.Close()
	request := func(want int) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, proxy.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer client-token")
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
