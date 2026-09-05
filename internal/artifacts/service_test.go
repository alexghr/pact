package artifacts

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexghr/pact/internal/state"
)

func TestServiceBindsWritesToSession(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	creator := newTestService(t, store)
	editor := newTestService(t, store)
	artifact, err := creator.CreateArtifact(ctx, CreateArtifactInput{Name: "Diagram"})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte{0, 1, 2, 3}
	written, err := creator.WriteFile(ctx, WriteFileInput{
		ArtifactID: artifact.ID, Path: "images/diagram.bin",
		Content: base64.StdEncoding.EncodeToString(content), Encoding: "base64",
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetArtifactFile(ctx, artifact.ID, written.File.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored.Content, content) || stored.UpdatedByPactSessionID != creator.pactSessionID {
		t.Fatalf("stored file = %#v", stored)
	}
	_, err = editor.WriteFile(ctx, WriteFileInput{
		ArtifactID: artifact.ID, Path: stored.Path, Content: "another session's edit",
		ExpectedVersion: stored.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetArtifact(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CreatorPactSessionID != creator.pactSessionID || updated.UpdatedByPactSessionID != editor.pactSessionID {
		t.Fatalf("cross-session edit changed attribution: %#v", updated)
	}
	if updated.Files[0].UpdatedByPactSessionID != editor.pactSessionID {
		t.Fatalf("file editor = %d", updated.Files[0].UpdatedByPactSessionID)
	}
}

func TestEditFileChecksVersionAndExactMatches(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	service := newTestService(t, store)
	artifact, err := service.CreateArtifact(ctx, CreateArtifactInput{Name: "Plan"})
	if err != nil {
		t.Fatal(err)
	}
	written, err := service.WriteFile(ctx, WriteFileInput{
		ArtifactID: artifact.ID, Path: "plan.md", Content: "draft: draft",
	})
	if err != nil {
		t.Fatal(err)
	}
	input := EditFileInput{
		ArtifactID: artifact.ID, Path: written.File.Path,
		OldText: "draft", NewText: "ready", ExpectedVersion: written.File.Version,
	}
	if _, err := service.EditFile(ctx, input); err == nil {
		t.Fatal("ambiguous edit succeeded")
	}
	input.ReplaceAll = true
	if _, err := service.EditFile(ctx, input); err != nil {
		t.Fatal(err)
	}
	// Even an exact match must not authorize an edit based on an old read.
	input.OldText = "ready"
	input.NewText = "stale"
	if _, err := service.EditFile(ctx, input); !errors.Is(err, state.ErrArtifactConflict) {
		t.Fatalf("stale edit error = %v", err)
	}
	input.ExpectedVersion = 0
	if _, err := service.EditFile(ctx, input); !errors.Is(err, state.ErrArtifactConflict) {
		t.Fatalf("edit without a version error = %v", err)
	}
	file, err := store.GetArtifactFile(ctx, artifact.ID, input.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(file.Content) != "ready: ready" || file.Version != written.File.Version+1 {
		t.Fatalf("stored file after edits = %#v", file)
	}
	input.ExpectedVersion = file.Version
	input.OldText = "ready: ready"
	input.NewText = "approved"
	input.ReplaceAll = false
	if _, err := service.EditFile(ctx, input); err != nil {
		t.Fatal(err)
	}
	file, err = store.GetArtifactFile(ctx, artifact.ID, input.Path)
	if err != nil || string(file.Content) != "approved" {
		t.Fatalf("exact edit content = %q, error = %v", file.Content, err)
	}
}

func TestEditFileBoundsExpansion(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	service := newTestService(t, store)
	artifact, err := service.CreateArtifact(ctx, CreateArtifactInput{Name: "Repeated text"})
	if err != nil {
		t.Fatal(err)
	}
	written, err := service.WriteFile(ctx, WriteFileInput{
		ArtifactID: artifact.ID, Path: "text.txt", Content: strings.Repeat("a", 1024),
	})
	if err != nil {
		t.Fatal(err)
	}
	input := EditFileInput{
		ArtifactID: artifact.ID, Path: written.File.Path,
		OldText: "a", NewText: strings.Repeat("b", state.MaxArtifactFileBytes/1024+1),
		ReplaceAll: true, ExpectedVersion: written.File.Version,
	}
	if _, err := service.EditFile(ctx, input); !errors.Is(err, state.ErrInvalidArtifactFile) {
		t.Fatalf("oversized edit error = %v", err)
	}
	unchanged, err := store.GetArtifact(ctx, artifact.ID)
	if err != nil || unchanged.Revision != written.ArtifactRevision {
		t.Fatalf("rejected edit changed artifact = %#v, error = %v", unchanged, err)
	}
	// The exact size limit is permitted; this also proves the rejected edit
	// left the original text and version available for the next edit.
	input.NewText = input.NewText[:len(input.NewText)-1]
	if _, err := service.EditFile(ctx, input); err != nil {
		t.Fatal(err)
	}
	file, err := store.GetArtifactFile(ctx, artifact.ID, input.Path)
	if err != nil || len(file.Content) != state.MaxArtifactFileBytes || bytes.Count(file.Content, []byte("b")) != len(file.Content) {
		t.Fatalf("bounded edit size = %d, error = %v", len(file.Content), err)
	}
}

func openTestStore(t *testing.T) *state.Store {
	t.Helper()
	store, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "pact.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return store
}

func newTestService(t *testing.T, store *state.Store) *Service {
	t.Helper()
	sessionID, err := store.CreateSession(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewService(store, sessionID)
}
