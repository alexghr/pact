package imagebuilder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alexghr/pact/internal/docker"
)

type fakeImageBuilder struct {
	mu       sync.Mutex
	images   map[string]bool
	builds   []docker.BuildOptions
	buildErr error
}

func (f *fakeImageBuilder) Build(_ context.Context, options docker.BuildOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.builds = append(f.builds, options)
	if f.buildErr != nil {
		return f.buildErr
	}
	f.images[options.Tag] = true
	return nil
}

func (f *fakeImageBuilder) ImageExists(_ context.Context, image string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.images[image], nil
}

func TestOnDemandReusesExistingContentImage(t *testing.T) {
	profile := testProfile(t)
	fingerprint, err := buildFingerprint(profile)
	if err != nil {
		t.Fatal(err)
	}
	want := profile.Build.Tag + "-" + fingerprint
	engine := &fakeImageBuilder{images: map[string]bool{want: true}}
	builder := NewOnDemand(engine, []Profile{profile})

	got, err := builder.Resolve(context.Background(), profile.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Resolve() = %q, want %q", got, want)
	}
	if len(engine.builds) != 0 {
		t.Fatalf("builds = %#v, want no build", engine.builds)
	}
}

func TestOnDemandBuildsAgainOnlyAfterContextChanges(t *testing.T) {
	profile := testProfile(t)
	engine := &fakeImageBuilder{images: make(map[string]bool)}
	builder := NewOnDemand(engine, []Profile{profile})

	first, err := builder.Resolve(context.Background(), profile.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Resolve(context.Background(), profile.Name); err != nil {
		t.Fatal(err)
	}
	if len(engine.builds) != 1 {
		t.Fatalf("unchanged builds = %d, want 1", len(engine.builds))
	}

	if err := os.WriteFile(filepath.Join(profile.Build.ContextDir, "entrypoint.sh"), []byte("changed\n"), 0700); err != nil {
		t.Fatal(err)
	}
	second, err := builder.Resolve(context.Background(), profile.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(engine.builds) != 2 {
		t.Fatalf("changed builds = %d, want 2", len(engine.builds))
	}
	if first == second {
		t.Fatalf("image remained %q after a context change", first)
	}
	if engine.builds[1].Tag != second {
		t.Fatalf("second build tag = %q, want %q", engine.builds[1].Tag, second)
	}
}

func TestOnDemandReportsBuildFailureAndAllowsRetry(t *testing.T) {
	profile := testProfile(t)
	engine := &fakeImageBuilder{
		images:   make(map[string]bool),
		buildErr: errors.New("invalid Dockerfile"),
	}
	builder := NewOnDemand(engine, []Profile{profile})

	_, err := builder.Resolve(context.Background(), profile.Name)
	if err == nil || !strings.Contains(err.Error(), "invalid Dockerfile") {
		t.Fatalf("Resolve() error = %v", err)
	}

	engine.buildErr = nil
	if _, err := builder.Resolve(context.Background(), profile.Name); err != nil {
		t.Fatalf("Resolve() after retry: %v", err)
	}
	if len(engine.builds) != 2 {
		t.Fatalf("build attempts = %d, want 2", len(engine.builds))
	}
}

func TestOnDemandReportsFingerprintFailure(t *testing.T) {
	profile := Profile{
		Name: "missing",
		Build: docker.BuildOptions{
			ContextDir: filepath.Join(t.TempDir(), "missing"),
			Tag:        "pact-codex:missing",
		},
	}
	builder := NewOnDemand(
		&fakeImageBuilder{images: make(map[string]bool)},
		[]Profile{profile},
	)
	if _, err := builder.Resolve(context.Background(), profile.Name); err == nil {
		t.Fatal("Resolve() succeeded with a missing build context")
	}
}

func TestOnDemandCoalescesMatchingPreparations(t *testing.T) {
	builder := NewOnDemand(
		&fakeImageBuilder{images: make(map[string]bool)},
		nil,
	)
	first, prepare := builder.begin("pact-codex:test-fingerprint")
	if !prepare {
		t.Fatal("first caller did not own image preparation")
	}
	second, prepare := builder.begin("pact-codex:test-fingerprint")
	if prepare || second != first {
		t.Fatal("second caller did not join the existing image preparation")
	}

	builder.finish("pact-codex:test-fingerprint", first, nil)
	select {
	case <-second.done:
	default:
		t.Fatal("shared image preparation did not notify its waiter")
	}
}

func testProfile(t *testing.T) Profile {
	t.Helper()
	contextDir := t.TempDir()
	dockerfile := filepath.Join(contextDir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, "entrypoint.sh"), []byte("initial\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return Profile{
		Name: "test",
		Build: docker.BuildOptions{
			ContextDir: contextDir,
			Dockerfile: dockerfile,
			Tag:        "pact-codex:test",
		},
	}
}
