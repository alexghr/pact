package web

import (
	"context"
	"mime"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/alexghr/pact/internal/state"
)

func TestArtifactFileViewAndDownload(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "pact.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.CreateSession(ctx, "/tmp/project")
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CreateArtifact(ctx, sessionID, "Notes", "")
	if err != nil {
		t.Fatal(err)
	}
	const content = "<script>alert(document.cookie)</script>\n# Raw text\n"
	_, _, err = store.WriteArtifactFile(ctx, state.ArtifactFileWrite{
		ArtifactID: artifact.ID, EditorPactSessionID: sessionID,
		Path: "notes/test.html", MediaType: "text/html", Content: []byte(content),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store}
	for _, tt := range []struct {
		name, query, mediaType, disposition string
	}{
		{"view", "?view=1", "text/plain; charset=utf-8", "inline"},
		{"download", "", "text/html", "attachment"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, artifactFileURL(artifact.ID, "notes/test.html")+tt.query, nil)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != content {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != tt.mediaType {
				t.Errorf("Content-Type = %q, want %q", got, tt.mediaType)
			}
			disposition, _, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
			if err != nil || disposition != tt.disposition {
				t.Errorf("Content-Disposition = %q, %v", disposition, err)
			}
			if response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Error("missing nosniff")
			}
			if tt.name == "view" && response.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" {
				t.Error("missing viewer sandbox policy")
			}
		})
	}
}
