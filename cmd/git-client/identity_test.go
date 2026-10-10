package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestConcurrentRefreshRetainsTrust(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var next atomic.Int32
	var invalid atomic.Bool
	var unchanged atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email := fmt.Sprintf("agent%d@example.test", next.Add(1))
		if unchanged.Load() {
			email = "agent8@example.test"
		}
		if invalid.Load() {
			email = "*@example.test"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"name": "Pact Agent", "email": email, "publicKey": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		})
	}))
	defer server.Close()
	t.Setenv("PACT_GIT_URL", server.URL)
	const count = 8
	done := make(chan error, count)
	for range count {
		go func() { done <- refreshIdentity() }()
	}
	for range count {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(os.Getenv("HOME"), ".config", "pact", "git-allowed-signers")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= count; i++ {
		if !strings.Contains(string(data), fmt.Sprintf("agent%d@example.test ", i)) {
			t.Fatalf("concurrent refresh lost identity %d: %s", i, data)
		}
	}
	unchanged.Store(true)
	for range 2 {
		if err := refreshIdentity(); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatalf("repeated refresh changed cached trust: %v, %s", err, after)
	}
	invalid.Store(true)
	if err := refreshIdentity(); err == nil {
		t.Fatal("accepted a wildcard signing identity")
	}
	after, err = os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatalf("failed refresh changed cached trust: %v, %s", err, after)
	}
}
