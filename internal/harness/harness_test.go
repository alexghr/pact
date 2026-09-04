package harness

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexghr/pact/internal/codex"
	"github.com/alexghr/pact/internal/state"
)

func TestRunnerRejectsInvalidPolicyOptions(t *testing.T) {
	runner := &Runner{}
	if _, err := runner.Run(context.Background(), 1, Options{Image: "generic"}, nil); err == nil ||
		!strings.Contains(err.Error(), "prompt must not be empty") {
		t.Fatalf("empty prompt error = %v", err)
	}
	if _, err := runner.Run(context.Background(), 1, Options{Prompt: "hello", Image: "../private"}, nil); err == nil ||
		!strings.Contains(err.Error(), "unsupported image") {
		t.Fatalf("unsupported image error = %v", err)
	}
}

func TestCanonicalWorkspace(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}

	got, err := canonicalWorkspace(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("canonicalWorkspace() = %q, want %q", got, want)
	}
}

func TestCanonicalWorkspaceRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalWorkspace(path); err == nil {
		t.Fatal("canonicalWorkspace() accepted a file")
	}
}

func TestCanonicalWorkspaceRejectsUnsafeMountPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsafe:path")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalWorkspace(path); err == nil {
		t.Fatal("canonicalWorkspace() accepted a path containing ':'")
	}
}

func TestNormalizeRepositoryDefaults(t *testing.T) {
	repository, err := normalizeRepository(state.Repository{
		URL: " https://github.com/acme/widgets ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.URL != "https://github.com/acme/widgets" ||
		repository.CloneURL != repository.URL || repository.PushURL != repository.CloneURL ||
		repository.Name != "widgets" || repository.DefaultBranch != "main" {
		t.Fatalf("normalizeRepository() = %#v", repository)
	}

	repository, err = normalizeRepository(state.Repository{
		URL:           "https://code.example/acme/widgets",
		CloneURL:      "git@code.example:acme/widgets.git",
		PushURL:       "ssh://git@code.example/fork/widgets.git",
		Name:          "custom name",
		DefaultBranch: "trunk",
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.Name != "custom name" || repository.DefaultBranch != "trunk" ||
		repository.PushURL != "ssh://git@code.example/fork/widgets.git" {
		t.Fatalf("normalizeRepository() overwrote explicit values: %#v", repository)
	}
}

func TestNormalizeRepositoryRejectsUnsafeURLs(t *testing.T) {
	tests := []state.Repository{
		{URL: "git@example.com:acme/widgets.git"},
		{URL: "https://token@example.com/acme/widgets"},
		{URL: "https://example.com/acme/widgets?token=secret"},
		{URL: "https://example.com/acme/widgets", CloneURL: "/tmp/widgets"},
		{URL: "https://example.com/acme/widgets", CloneURL: "ext::sh"},
		{URL: "https://example.com/acme/widgets", CloneURL: "-oProxyCommand=bad@example.com:widgets.git"},
		{URL: "https://example.com/acme/widgets", CloneURL: "https://token@example.com/widgets.git"},
		{URL: "https://example.com/acme/widgets", CloneURL: "ssh://git:secret@example.com/widgets.git"},
		{URL: "https://example.com/acme/widgets", CloneURL: "https://example.com/widgets.git?token=secret"},
		{URL: "https://example.com/acme/widgets", DefaultBranch: "main\n--upload-pack=bad"},
	}
	for _, repository := range tests {
		if _, err := normalizeRepository(repository); !errors.Is(err, ErrInvalidRepository) {
			t.Errorf("normalizeRepository(%#v) error = %v, want ErrInvalidRepository", repository, err)
		}
	}
}

func TestCreateRepositoryRejectsUnsafeCloneURL(t *testing.T) {
	store := openHarnessTestStore(t)
	runner := &Runner{store: store}

	_, err := runner.CreateRepository(context.Background(), state.Repository{
		URL:      "https://github.com/acme/widgets",
		CloneURL: "ext::sh -c exploit",
	})
	if !errors.Is(err, ErrInvalidRepository) {
		t.Fatalf("CreateRepository() error = %v, want ErrInvalidRepository", err)
	}
	repositories, err := store.ListRepositories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 0 {
		t.Fatalf("invalid repository was stored = %#v", repositories)
	}
}

func TestRepositoryName(t *testing.T) {
	for cloneURL, want := range map[string]string{
		"https://github.com/acme/widgets.git": "widgets",
		"ssh://git@example.com/acme/widgets":  "widgets",
		"git@example.com:acme/widgets.git":    "widgets",
	} {
		if got := repositoryName(cloneURL); got != want {
			t.Errorf("repositoryName(%q) = %q, want %q", cloneURL, got, want)
		}
	}
}

func TestCreateSessionWithRepositories(t *testing.T) {
	store := openHarnessTestStore(t)
	repositoryID, err := store.CreateRepository(context.Background(), state.Repository{
		URL:           "https://example.com/acme/widgets",
		CloneURL:      "git@example.com:acme/widgets.git",
		PushURL:       "git@example.com:fork/widgets.git",
		Name:          "widgets",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	checkout := &fakeRepositoryCheckout{}
	runner := &Runner{store: store, storageRoot: t.TempDir(), checkout: checkout}

	sessionID, workspace, err := runner.CreateSession(context.Background(), SessionOptions{
		RepositoryIDs: []int64{repositoryID, repositoryID},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	if len(checkout.calls) != 2 {
		t.Fatalf("checkout calls = %#v", checkout.calls)
	}
	checkoutDirs := make([]string, 0, len(checkout.calls))
	for i, call := range checkout.calls {
		checkoutDir := filepath.Base(call.workspace)
		checkoutDirs = append(checkoutDirs, checkoutDir)
		checkoutWorkspace, err := canonicalWorkspace(checkout.calls[i].workspace)
		if err != nil {
			t.Fatal(err)
		}
		if call.repository.ID != repositoryID || !strings.HasPrefix(checkoutDir, "widgets-") ||
			checkoutWorkspace != filepath.Join(workspace, checkoutDir) {
			t.Fatalf("checkout %d = %#v", i, call)
		}
		if _, err := os.Stat(filepath.Join(workspace, checkoutDir, "README.md")); err != nil {
			t.Fatalf("cloned workspace %q: %v", checkoutDir, err)
		}
	}
	session, err := store.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if checkoutDirs[0] == checkoutDirs[1] || len(session.Repositories) != 2 ||
		session.Repositories[0].Repository.ID != repositoryID ||
		session.Repositories[0].CheckoutDir != checkoutDirs[0] ||
		session.Repositories[1].Repository.ID != repositoryID ||
		session.Repositories[1].CheckoutDir != checkoutDirs[1] || session.WorkspaceDir != workspace {
		t.Fatalf("repository session = %#v", session)
	}
}

func TestCreateSessionCleansFailedCheckout(t *testing.T) {
	store := openHarnessTestStore(t)
	repositoryID, err := store.CreateRepository(context.Background(), state.Repository{
		URL:           "https://example.com/acme/widgets",
		CloneURL:      "https://example.com/acme/widgets.git",
		PushURL:       "https://example.com/acme/widgets.git",
		Name:          "widgets",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	checkout := &fakeRepositoryCheckout{err: errors.New("clone failed")}
	runner := &Runner{store: store, storageRoot: t.TempDir(), checkout: checkout}

	if _, _, err := runner.CreateSession(context.Background(), SessionOptions{
		RepositoryIDs: []int64{repositoryID},
	}); err == nil {
		t.Fatal("CreateSession() succeeded")
	}
	if len(checkout.calls) != 1 {
		t.Fatalf("checkout calls = %#v", checkout.calls)
	}
	if _, err := os.Stat(filepath.Dir(checkout.calls[0].workspace)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed checkout workspace still exists: %v", err)
	}
	sessions, err := store.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("failed checkout created sessions = %#v", sessions)
	}
}

func TestCreateSessionWithExplicitWorkspace(t *testing.T) {
	store := openHarnessTestStore(t)
	runner := &Runner{store: store}
	workspaceDir := t.TempDir()

	sessionID, workspace, err := runner.CreateSession(context.Background(), SessionOptions{
		Workspace: workspaceDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantWorkspace, err := canonicalWorkspace(workspaceDir)
	if err != nil {
		t.Fatal(err)
	}
	if workspace != wantWorkspace {
		t.Fatalf("workspace = %q, want %q", workspace, wantWorkspace)
	}
	session, err := store.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.WorkspaceDir != wantWorkspace || len(session.Repositories) != 0 {
		t.Fatalf("session = %#v", session)
	}

	if _, _, err := runner.CreateSession(context.Background(), SessionOptions{
		Workspace:     workspaceDir,
		RepositoryIDs: []int64{1},
	}); err == nil {
		t.Fatal("CreateSession() accepted repositories with an explicit workspace")
	}
}

func TestCreateSessionWithManagedWorkspace(t *testing.T) {
	store := openHarnessTestStore(t)
	runner := &Runner{store: store, storageRoot: t.TempDir()}

	sessionID, workspace, err := runner.CreateSession(context.Background(), SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	info, err := os.Stat(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("managed workspace %q is not a directory", workspace)
	}
	session, err := store.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.WorkspaceDir != workspace || len(session.Repositories) != 0 {
		t.Fatalf("session = %#v", session)
	}
}

func TestHostGitCheckoutKeepsLocalHistoryAndOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	source := filepath.Join(t.TempDir(), "source")
	runGit(t, "init", "--initial-branch=main", source)
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", source, "add", "README.md")
	runGit(t, "-C", source, "-c", "user.name=Pact Test", "-c", "user.email=pact@example.com", "-c", "commit.gpgsign=false", "commit", "-m", "initial")

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	err := (hostGitCheckout{}).Clone(context.Background(), state.Repository{
		CloneURL:      source,
		PushURL:       "this URL must not be used",
		DefaultBranch: "main",
	}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, "-C", workspace, "log", "-1", "--format=%s"); got != "initial" {
		t.Fatalf("cloned history = %q, want initial", got)
	}
	if got := runGit(t, "-C", workspace, "remote", "get-url", "origin"); got != source {
		t.Fatalf("origin = %q, want %q", got, source)
	}
}

func TestValidateResumeTarget(t *testing.T) {
	err := validateResumeTarget(8, "/tmp/project", &state.ResumeTarget{
		PactSessionID: 7,
		WorkspaceDir:  "/tmp/project",
	})
	if err == nil || !strings.Contains(err.Error(), "belongs to session 7") {
		t.Fatalf("validateResumeTarget() cross-session error = %v", err)
	}
	err = validateResumeTarget(7, "/tmp/other", &state.ResumeTarget{
		PactSessionID: 7,
		WorkspaceDir:  "/tmp/project",
	})
	if err == nil || !strings.Contains(err.Error(), "belongs to workspace") {
		t.Fatalf("validateResumeTarget() workspace error = %v", err)
	}
}

func TestProcessExitCode(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 23").Run()
	code := processExitCode(err)
	if code == nil || *code != 23 {
		t.Fatalf("processExitCode() = %v, want 23", code)
	}
}

func TestProcessExitCodeUnknown(t *testing.T) {
	if code := processExitCode(errors.New("docker unavailable")); code != nil {
		t.Fatalf("processExitCode() = %d, want nil", *code)
	}
}

func TestWaitForCodexTurnStreamsAgentMessage(t *testing.T) {
	messages := strings.NewReader(
		`{"method":"item/agentMessage/delta","params":{"threadId":"other","turnId":"other","delta":"ignore"}}` + "\n" +
			`{"method":"item/agentMessage/delta","params":{"threadId":"thr_123","turnId":"turn_456","delta":"done"}}` + "\n" +
			`{"method":"turn/completed","params":{"threadId":"thr_123","turn":{"id":"other","items":[],"status":"completed"}}}` + "\n" +
			`{"method":"turn/completed","params":{"threadId":"thr_123","turn":{"id":"turn_456","items":[],"status":"completed"}}}` + "\n",
	)
	client := codex.NewClient(messages, io.Discard)
	var output bytes.Buffer

	if err := waitForCodexTurn(context.Background(), client, &output, "thr_123", "turn_456", nil); err != nil {
		t.Fatal(err)
	}
	if output.String() != "done" {
		t.Fatalf("agent output = %q, want done", output.String())
	}
}

func TestWaitForCodexTurnReturnsFailure(t *testing.T) {
	messages := strings.NewReader(
		`{"method":"turn/completed","params":{"threadId":"thr_123","turn":{"id":"turn_456","items":[],"status":"failed","error":{"message":"model unavailable"}}}}` + "\n",
	)
	client := codex.NewClient(messages, io.Discard)

	err := waitForCodexTurn(context.Background(), client, io.Discard, "thr_123", "turn_456", nil)
	if err == nil || !strings.Contains(err.Error(), "model unavailable") {
		t.Fatalf("waitForCodexTurn() error = %v", err)
	}
}

func TestShouldRecordCodexEvent(t *testing.T) {
	for _, method := range []string{
		"item/completed",
		"turn/completed",
		"thread/tokenUsage/updated",
		"model/rerouted",
	} {
		if !shouldRecordCodexEvent(codex.Message{Method: method}) {
			t.Errorf("shouldRecordCodexEvent(%q) = false", method)
		}
	}
	for _, method := range []string{
		"item/agentMessage/delta",
		"item/commandExecution/outputDelta",
		"turn/diff/updated",
	} {
		if shouldRecordCodexEvent(codex.Message{Method: method}) {
			t.Errorf("shouldRecordCodexEvent(%q) = true", method)
		}
	}
}

type repositoryCheckoutCall struct {
	repository state.Repository
	workspace  string
}

type fakeRepositoryCheckout struct {
	calls []repositoryCheckoutCall
	err   error
}

func (f *fakeRepositoryCheckout) Clone(_ context.Context, repository state.Repository, workspace string) error {
	f.calls = append(f.calls, repositoryCheckoutCall{repository: repository, workspace: workspace})
	if f.err != nil {
		return f.err
	}
	if err := os.MkdirAll(workspace, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workspace, "README.md"), []byte("checkout\n"), 0600)
}

func openHarnessTestStore(t *testing.T) *state.Store {
	t.Helper()
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "pact.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func runGit(t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
