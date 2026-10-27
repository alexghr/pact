package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAuthSnapshotExcludesRenewableAndUnrelatedCredentials(t *testing.T) {
	source := filepath.Join(t.TempDir(), "auth.json")
	login := []byte(`{
		"auth_mode":"chatgpt",
		"OPENAI_API_KEY":"unrelated-api-secret",
		"futureCredential":"unknown-top-level-secret",
		"tokens":{
			"id_token":"identity-token", "access_token":"access-token",
			"refresh_token":"renewable-secret", "account_id":"account",
			"futureCredential":"unknown-nested-secret"
		},
		"last_refresh":"2020-01-01T00:00:00Z"
	}`)
	if err := os.WriteFile(source, login, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := CreateAuthSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(snapshot) })
	data, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"unrelated-api-secret", "unknown-top-level-secret", "renewable-secret", "unknown-nested-secret"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("authentication snapshot leaked an excluded credential")
		}
	}
	if !bytes.Contains(data, []byte("access-token")) || !bytes.Contains(data, []byte("2020-01-01T00:00:00Z")) {
		t.Fatal("authentication snapshot lost access or masked native staleness checks")
	}
	info, err := os.Stat(snapshot)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatalf("snapshot must be private to its owner: %v", err)
	}
	unchanged, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(login, unchanged) {
		t.Fatalf("snapshot creation changed the host login: %v", err)
	}
}
