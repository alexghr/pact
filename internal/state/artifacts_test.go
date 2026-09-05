package state

import (
	"context"
	"errors"
	"testing"
)

func TestArtifactLifecycleAcrossSessions(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	creatorSessionID := createTestSession(t, store, "/tmp/creator")
	editorSessionID := createTestSession(t, store, "/tmp/editor")

	artifact, err := store.CreateArtifact(ctx, creatorSessionID, " Release notes ", " September release ")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.CreatorPactSessionID != creatorSessionID ||
		artifact.UpdatedByPactSessionID != creatorSessionID ||
		artifact.Name != "Release notes" || artifact.Description != "September release" ||
		artifact.Revision != 1 || len(artifact.Files) != 0 {
		t.Fatalf("CreateArtifact() = %#v", artifact)
	}

	file, revision, err := store.WriteArtifactFile(ctx, ArtifactFileWrite{
		ArtifactID:          artifact.ID,
		EditorPactSessionID: creatorSessionID,
		Path:                "notes/release.txt",
		MediaType:           "text/plain",
		Content:             []byte("first draft"),
		ExpectedVersion:     0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision != 2 || file.Version != 1 || file.SizeBytes != 11 ||
		file.UpdatedByPactSessionID != creatorSessionID {
		t.Fatalf("first WriteArtifactFile() = %#v, revision %d", file, revision)
	}

	artifacts, err := store.SearchArtifacts(ctx, ArtifactSearch{Query: "release.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts.Artifacts) != 1 || artifacts.Artifacts[0].ID != artifact.ID ||
		artifacts.Artifacts[0].FileCount != 1 || artifacts.Artifacts[0].SizeBytes != 11 {
		t.Fatalf("SearchArtifacts() = %#v", artifacts)
	}
	creatorArtifacts, err := store.SearchArtifacts(ctx, ArtifactSearch{CreatorPactSessionID: creatorSessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(creatorArtifacts.Artifacts) != 1 || creatorArtifacts.Artifacts[0].ID != artifact.ID {
		t.Fatalf("creator SearchArtifacts() = %#v", creatorArtifacts)
	}
	otherArtifacts, err := store.SearchArtifacts(ctx, ArtifactSearch{CreatorPactSessionID: editorSessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(otherArtifacts.Artifacts) != 0 {
		t.Fatalf("editor-owned SearchArtifacts() = %#v", otherArtifacts)
	}

	chunk, err := store.ReadArtifactFile(ctx, artifact.ID, "notes/release.txt", 6, 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(chunk.Content) != "draft" || chunk.Offset != 6 || chunk.Version != 1 {
		t.Fatalf("ReadArtifactFile() = %#v, content %q", chunk, chunk.Content)
	}

	updatedName := "Final release notes"
	updated, err := store.UpdateArtifact(ctx, ArtifactUpdate{
		ID:                  artifact.ID,
		EditorPactSessionID: editorSessionID,
		Name:                &updatedName,
		ExpectedRevision:    revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CreatorPactSessionID != creatorSessionID ||
		updated.UpdatedByPactSessionID != editorSessionID ||
		updated.Name != updatedName || updated.Revision != 3 {
		t.Fatalf("UpdateArtifact() = %#v", updated)
	}

	expectedVersion := file.Version
	file, revision, err = store.WriteArtifactFile(ctx, ArtifactFileWrite{
		ArtifactID:          artifact.ID,
		EditorPactSessionID: editorSessionID,
		Path:                "notes/release.txt",
		MediaType:           "text/plain",
		Content:             []byte("final draft"),
		ExpectedVersion:     expectedVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision != 4 || file.Version != 2 || file.UpdatedByPactSessionID != editorSessionID {
		t.Fatalf("cross-session WriteArtifactFile() = %#v, revision %d", file, revision)
	}

	if _, _, err := store.WriteArtifactFile(ctx, ArtifactFileWrite{
		ArtifactID:          artifact.ID,
		EditorPactSessionID: creatorSessionID,
		Path:                "notes/release.txt",
		MediaType:           "text/plain",
		Content:             []byte("stale edit"),
		ExpectedVersion:     expectedVersion,
	}); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("stale WriteArtifactFile() error = %v, want ErrArtifactConflict", err)
	}
	stored, err := store.GetArtifact(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != 4 || len(stored.Files) != 1 || stored.Files[0].Version != 2 {
		t.Fatalf("stale write changed artifact = %#v", stored)
	}
	content, err := store.GetArtifactFile(ctx, artifact.ID, "notes/release.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(content.Content) != "final draft" {
		t.Fatalf("stored content = %q", content.Content)
	}
}

func TestArtifactRejectsUnknownSessionAndUnsafeFilePaths(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if _, err := store.CreateArtifact(ctx, 99, "orphan", ""); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("CreateArtifact() error = %v, want ErrSessionNotFound", err)
	}

	sessionID := createTestSession(t, store, "/tmp/project")
	artifact, err := store.CreateArtifact(ctx, sessionID, "safe", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, filePath := range []string{"", "..", "/absolute", "../escape", "a/../escape", "a//b", "a\\b", "a\nb"} {
		if _, _, err := store.WriteArtifactFile(ctx, ArtifactFileWrite{
			ArtifactID:          artifact.ID,
			EditorPactSessionID: sessionID,
			Path:                filePath,
			MediaType:           "text/plain",
			Content:             []byte("unsafe"),
		}); !errors.Is(err, ErrInvalidArtifactFile) {
			t.Errorf("WriteArtifactFile(path %q) error = %v, want ErrInvalidArtifactFile", filePath, err)
		}
	}
}

func TestArtifactMutationsRequireObservedVersions(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	sessionID := createTestSession(t, store, "/tmp/project")
	artifact, err := store.CreateArtifact(ctx, sessionID, "Plan", "Keep this description")
	if err != nil {
		t.Fatal(err)
	}
	write := ArtifactFileWrite{
		ArtifactID: artifact.ID, EditorPactSessionID: sessionID,
		Path: "plan.md", MediaType: "text/markdown", Content: []byte("reviewed text"),
	}
	file, revision, err := store.WriteArtifactFile(ctx, write)
	if err != nil {
		t.Fatal(err)
	}
	// Zero is create-only, including when the caller omits ExpectedVersion.
	write.Content = []byte("accidental replacement")
	if _, _, err := store.WriteArtifactFile(ctx, write); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("replacement without a version error = %v", err)
	}
	// Two writers starting from the same version cannot both succeed.
	write.ExpectedVersion = file.Version
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, _, err := store.WriteArtifactFile(ctx, write)
			results <- err
		}()
	}
	close(start)
	var successes, conflicts int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrArtifactConflict):
			conflicts++
		default:
			t.Fatalf("concurrent write error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent writes: %d successes, %d conflicts", successes, conflicts)
	}
	name := "Changed plan"
	update := ArtifactUpdate{
		ID: artifact.ID, EditorPactSessionID: sessionID, Name: &name,
	}
	if _, err := store.UpdateArtifact(ctx, update); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("metadata update without a revision error = %v", err)
	}
	update.ExpectedRevision = revision
	if _, err := store.UpdateArtifact(ctx, update); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("stale metadata update error = %v", err)
	}
	unchanged, err := store.GetArtifact(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Name != artifact.Name || unchanged.Revision != revision+1 || unchanged.Files[0].Version != file.Version+1 {
		t.Fatalf("rejected writes changed artifact: %#v", unchanged)
	}
	// A valid metadata edit may explicitly clear the description.
	empty := ""
	update.ExpectedRevision = unchanged.Revision
	update.Description = &empty
	if _, err := store.UpdateArtifact(ctx, update); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetArtifact(ctx, artifact.ID)
	if err != nil || updated.Name != name || updated.Description != "" {
		t.Fatalf("metadata after valid update = %#v, error = %v", updated, err)
	}
}

func TestArtifactSearchPaginatesWithoutReorderingEdits(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	sessionID := createTestSession(t, store, "/tmp/project")
	var artifacts []Artifact
	for range 3 {
		artifact, err := store.CreateArtifact(ctx, sessionID, "Plan", "")
		if err != nil {
			t.Fatal(err)
		}
		artifacts = append(artifacts, artifact)
	}
	otherID := createTestSession(t, store, "/tmp/other")
	if _, err := store.CreateArtifact(ctx, otherID, "Plan", ""); err != nil {
		t.Fatal(err)
	}
	search := ArtifactSearch{Query: "plan", CreatorPactSessionID: sessionID, Limit: 2}
	first, err := store.SearchArtifacts(ctx, search)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Artifacts) != 2 || first.Artifacts[0].ID != artifacts[2].ID || first.Artifacts[1].ID != artifacts[1].ID || first.NextOffset == nil {
		t.Fatalf("first page = %#v", first)
	}
	description := "Edited while browsing"
	if _, err := store.UpdateArtifact(ctx, ArtifactUpdate{
		ID: artifacts[0].ID, EditorPactSessionID: sessionID,
		Description: &description, ExpectedRevision: artifacts[0].Revision,
	}); err != nil {
		t.Fatal(err)
	}
	search.Offset = *first.NextOffset
	second, err := store.SearchArtifacts(ctx, search)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Artifacts) != 1 || second.Artifacts[0].ID != artifacts[0].ID || second.NextOffset != nil {
		t.Fatalf("second page after edit = %#v", second)
	}
	for _, invalid := range []ArtifactSearch{{Limit: -1}, {Offset: -1}, {CreatorPactSessionID: -1}} {
		if _, err := store.SearchArtifacts(ctx, invalid); !errors.Is(err, ErrInvalidArtifact) {
			t.Errorf("search %#v error = %v", invalid, err)
		}
	}
}
