package state

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExecutionOwnership(t *testing.T) {
	if id := os.Getenv("PACT_TEST_OWNER_SESSION"); id != "" {
		store := openStore(t)
		sessionID, _ := strconv.ParseInt(id, 10, 64)
		launch, _, err := store.ResumeLaunch(t.Context(), sessionID, false)
		if err != nil {
			t.Fatal(err)
		}
		unlock, err := store.LockExecution(launch)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		fmt.Println("owned")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		return
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	first, err := store.CreateLaunch(t.Context(), LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestExecutionOwnership$")
	child.Env = append(os.Environ(), fmt.Sprintf("PACT_TEST_OWNER_SESSION=%d", first.SessionID))
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Process.Kill(); child.Wait() })
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "owned\n" {
		t.Fatalf("child ownership: %q, %v", line, err)
	}
	competing, err := store.CreateLaunch(t.Context(), LaunchOptions{Workspace: first.Workspace, Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if unlock, err := store.LockExecution(competing); err == nil {
		unlock()
		t.Fatal("competing workspace writer was allowed")
	} else if !strings.Contains(err.Error(), fmt.Sprintf("Pact session %d", first.SessionID)) {
		t.Fatalf("busy error did not identify owner: %v", err)
	}
	independent, err := store.CreateLaunch(t.Context(), LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := store.LockExecution(independent)
	if err != nil {
		t.Fatalf("independent workspace blocked: %v", err)
	}
	unlock()
	independent.Environment = first.Environment
	if unlock, err := store.LockExecution(independent); err == nil {
		unlock()
		t.Fatal("shared environment writer was allowed")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	unlock, err = store.LockExecution(first)
	if err != nil {
		t.Fatalf("crashed owner retained OS lock: %v", err)
	}
	unlock()
}

func TestWorkspaceRemovalSerializesResume(t *testing.T) {
	if id := os.Getenv("PACT_TEST_REMOVAL_SESSION"); id != "" {
		store := openStore(t)
		sessionID, _ := strconv.ParseInt(id, 10, 64)
		fmt.Println("waiting")
		unlock, err := store.LockStorage(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		if _, _, err := store.ResumeLaunch(t.Context(), sessionID, false); err == nil || !strings.Contains(err.Error(), "intentionally removed") {
			t.Fatalf("resume after cleanup entered a deleted directory: %v", err)
		}
		return
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	launch, err := store.CreateLaunch(t.Context(), LaunchOptions{Backend: "codex", Owner: fmt.Sprintf("uid:%d", os.Getuid())})
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := store.LockStorage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	release, err := store.LockExecution(launch)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestWorkspaceRemovalSerializesResume$")
	child.Env = append(os.Environ(), fmt.Sprintf("PACT_TEST_REMOVAL_SESSION=%d", launch.SessionID))
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Process.Kill(); child.Wait() })
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "waiting\n" {
		t.Fatalf("resume did not reach storage ownership: %q, %v", line, err)
	}
	if err := store.RemoveWorkspace(t.Context(), launch.Environment.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	release()
	unlock()
	if err := child.Wait(); err != nil {
		t.Fatalf("concurrent resume did not observe removal: %v", err)
	}
}
