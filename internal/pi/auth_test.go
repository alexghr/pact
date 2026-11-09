package pi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotsExcludeRenewableCredentialsAndPreserveCustomModels(t *testing.T) {
	agentDir := t.TempDir()
	login := []byte(`{
		"openai": {"type":"oauth", "access":"access-token", "refresh":"renewable-secret",
			"expires":1794185919426, "clientId":"issued-client", "scopes":["chatgpt.tokens.use.direct"],
			"futureCredential":"unknown-secret"},
		"mistral": {"type":"api_key", "key":"api-key", "refresh":"unrelated-secret"}
	}`)
	models := []byte(`{"providers":{"local-llama":{"baseUrl":"http://127.0.0.1:8080/v1",
		"api":"openai-completions","apiKey":"local","models":[{"id":"gpt-oss-20b",
		"samplingParams":{"temperature":1},"compat":{"thinkingTokenBudgetField":"thinking_budget_tokens"}}]}}}`)
	for name, data := range map[string][]byte{"auth.json": login, "models.json": models} {
		if err := os.WriteFile(filepath.Join(agentDir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	snapshots, err := createSnapshots(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(snapshots) })
	auth, err := os.ReadFile(filepath.Join(snapshots, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"renewable-secret", "unknown-secret", "unrelated-secret"} {
		if bytes.Contains(auth, []byte(secret)) {
			t.Fatal("authentication snapshot leaked an excluded credential")
		}
	}
	var credentials map[string]map[string]any
	if err := json.Unmarshal(auth, &credentials); err != nil {
		t.Fatal(err)
	}
	if credentials["openai"]["access"] != "access-token" || credentials["openai"]["refresh"] != "" ||
		credentials["openai"]["expires"] != float64(1794185919426) || credentials["mistral"]["key"] != "api-key" {
		t.Fatal("snapshot lost native authentication or expiry information")
	}
	for name, original := range map[string][]byte{"auth.json": login, "models.json": models} {
		unchanged, err := os.ReadFile(filepath.Join(agentDir, name))
		if err != nil || !bytes.Equal(unchanged, original) {
			t.Fatalf("snapshot creation changed host %s: %v", name, err)
		}
		info, err := os.Stat(filepath.Join(snapshots, name))
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s snapshot must be private to its owner: %v", name, err)
		}
	}
	projectedModels, err := os.ReadFile(filepath.Join(snapshots, "models.json"))
	if err != nil || !bytes.Equal(projectedModels, models) {
		t.Fatalf("custom model settings changed: %v", err)
	}
}

func TestSnapshotsWithoutHostLogin(t *testing.T) {
	// A local provider can supply its own key in models.json. Missing host
	// files must also mask any stale config in a resumed state volume.
	snapshots, err := createSnapshots(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(snapshots) })
	for _, name := range []string{"auth.json", "models.json"} {
		data, err := os.ReadFile(filepath.Join(snapshots, name))
		if err != nil || string(data) != "{}" {
			t.Fatalf("missing %s must produce an empty snapshot: %v", name, err)
		}
	}
}

func TestInvalidLoginErrorDoesNotExposeCredentials(t *testing.T) {
	source := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(source, []byte(`{"openai":{"type":"oauth","expires":"private-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := authSnapshot(source)
	if err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("invalid login must fail without credential details: %v", err)
	}
}
