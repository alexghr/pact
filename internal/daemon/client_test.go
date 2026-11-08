package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/session"
)

func TestMissingDaemonHint(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	for _, createDirectory := range []bool{false, true} {
		if createDirectory {
			if _, err := socketPath(true); err != nil {
				t.Fatal(err)
			}
		}
		err := Start(t.Context(), session.Options{}, false)
		if err == nil || !strings.Contains(err.Error(), "start it with pact daemon") {
			t.Fatalf("missing daemon error (directory=%t): %v", createDirectory, err)
		}
	}
}

func TestRestoreTerminalModes(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is required to check actual terminal modes")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	socket := filepath.Join(t.TempDir(), "tmux")
	command := func(args ...string) string {
		t.Helper()
		output, err := exec.CommandContext(ctx, tmux, append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux: %v: %s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	command("-f", "/dev/null", "new-session", "-d", "-s", "test", "sleep", "60")
	defer exec.Command(tmux, "-S", socket, "kill-server").Run()
	terminal, err := os.OpenFile(command("display-message", "-p", "#{pane_tty}"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	saved, err := stty(terminal, "-g")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stty(terminal, "raw", "-echo"); err != nil {
		t.Fatal(err)
	}
	awaitModes := func(want string) {
		t.Helper()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			got := command("display-message", "-p", "#{mouse_any_flag} #{mouse_sgr_flag} #{cursor_flag}")
			if got == want {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatalf("terminal modes = %q, want %q", got, want)
			case <-ticker.C:
			}
		}
	}
	_, _ = terminal.WriteString("\x1b[?1003h\x1b[?1006h\x1b[?25l")
	awaitModes("1 1 0")
	restoreTerminal(terminal, saved)
	awaitModes("0 0 1")
	if got, err := stty(terminal, "-g"); err != nil || got != saved {
		t.Fatalf("terminal input settings were not restored: %q, %v", got, err)
	}
}
