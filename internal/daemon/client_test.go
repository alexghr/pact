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
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	socket := filepath.Join(t.TempDir(), "tmux")
	command := func(t *testing.T, args ...string) string {
		t.Helper()
		output, err := exec.CommandContext(ctx, tmux, append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux: %v: %s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	command(t, "-f", "/dev/null", "new-session", "-d", "-s", "test", "sleep", "60")
	defer exec.Command(tmux, "-S", socket, "kill-server").Run()
	terminal, err := os.OpenFile(command(t, "display-message", "-p", "#{pane_tty}"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	saved, err := stty(terminal, "-g")
	if err != nil {
		t.Fatal(err)
	}
	for _, screen := range []string{"main", "alternate"} {
		t.Run(screen, func(t *testing.T) {
			if _, err := stty(terminal, "raw", "-echo"); err != nil {
				t.Fatal(err)
			}
			awaitScreen := func(wantModes, wantContent string) {
				t.Helper()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					modes := command(t, "display-message", "-p", "#{mouse_any_flag} #{mouse_sgr_flag} #{cursor_flag} #{cursor_x} #{cursor_y}")
					content := command(t, "capture-pane", "-p")
					if modes == wantModes && content == wantContent {
						return
					}
					select {
					case <-ctx.Done():
						t.Fatalf("terminal modes/content = %q / %q, want %q / %q", modes, content, wantModes, wantContent)
					case <-ticker.C:
					}
				}
			}
			output := "\x1b[Hstale main screen"
			if screen == "alternate" {
				output += "\x1b[?1049h"
			}
			output += "\x1b[2J\x1b[3;1Hstale TUI\x1b[?1003h\x1b[?1006h\x1b[?25l"
			if _, err := terminal.WriteString(output); err != nil {
				t.Fatal(err)
			}
			awaitScreen("1 1 0 9 2", "stale TUI")
			restoreTerminal(terminal, saved)
			awaitScreen("0 0 1 0 0", "")
			if got, err := stty(terminal, "-g"); err != nil || got != saved {
				t.Fatalf("terminal input settings were not restored: %q, %v", got, err)
			}
		})
	}
}
