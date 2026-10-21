package codex

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/alexghr/pact/internal/state"
)

// These events were captured from Codex 0.162.1. The prompt was a disposable
// probe; only its answer and native UUID are retained here.
const startedEvent = `{"type":"thread.started","thread_id":"01a14eba-7c54-7170-96ec-8d86bb280eec"}` + "\n"
const successfulTurn = `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"PACT_DISCOVERY_A"}}
{"type":"turn.completed","usage":{"input_tokens":14302,"cached_input_tokens":12288,"cache_write_input_tokens":0,"output_tokens":8,"reasoning_output_tokens":0}}
`

func TestExecPersistsBeforeStreamEnds(t *testing.T) {
	for _, scenario := range []struct {
		name, suffix string
		success      bool
		cancelled    bool
	}{
		{"completed", successfulTurn, true, false},
		{"recovered", "{\"type\":\"error\",\"message\":\"reconnecting\"}\n" + successfulTurn, true, false},
		{"cancelled", "", false, true},
		{"failed", "{\"type\":\"turn.failed\",\"error\":{\"message\":\"model unavailable\"}}\n", false, false},
		{"truncated", "{truncated", false, false},
		{"future event", "{\"type\":\"future.event\"}\n", false, false},
		{"future completed", "{\"type\":\"future.event\"}\n" + successfulTurn, true, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			store, err := state.Open(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			launch, err := store.CreateLaunch(t.Context(), state.LaunchOptions{
				Workspace: t.TempDir(), Owner: "operator", Backend: "codex",
			})
			if err != nil {
				t.Fatal(err)
			}
			reader, err := state.Open(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reader.Close() })
			input, writer := io.Pipe()
			var output, diagnostics bytes.Buffer
			done := make(chan error, 1)
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			if scenario.cancelled {
				cancel() // A UUID already received from the process must survive cancellation.
			}
			go func() {
				err := ConsumeExec(ctx, input, store, launch, &output, &diagnostics, "01a14eba-7c54-7170-96ec-8d86bb280eec")
				_ = input.Close()
				done <- err
				close(done)
			}()
			t.Cleanup(func() {
				_ = writer.Close()
				_ = input.Close()
				<-done
			})
			if _, err := io.WriteString(writer, startedEvent); err != nil {
				t.Fatal(err)
			}
			// This write waits for the next read, after thread.started was handled.
			// The stream remains open while a separate connection checks durability.
			if _, err := io.WriteString(writer, "{\"type\":\"turn.started\"}\n"); err != nil {
				t.Fatal(err)
			}
			threads, err := reader.Threads(t.Context(), launch.SessionID)
			if err != nil || len(threads) != 1 || threads[0].ID != "01a14eba-7c54-7170-96ec-8d86bb280eec" {
				t.Fatalf("thread not durable before EOF: %v, %v", threads, err)
			}
			lastUsed := threads[0].LastUsed
			if _, err := io.WriteString(writer, scenario.suffix); err != nil {
				t.Fatal(err)
			}
			_ = writer.Close()
			err = <-done
			if scenario.success {
				if err != nil || output.String() != "PACT_DISCOVERY_A\n" {
					t.Fatalf("successful output = %q, error = %v", output.String(), err)
				}
			} else if err == nil {
				t.Fatal("incomplete or unknown events reported success")
			}
			if scenario.name == "recovered" && !strings.Contains(diagnostics.String(), "reconnecting") {
				t.Fatal("recovered error was hidden from diagnostics")
			}
			if strings.Contains(diagnostics.String(), "future.event") {
				t.Fatal("unknown event produced a diagnostic")
			}
			threads, err = reader.Threads(t.Context(), launch.SessionID)
			if err != nil || len(threads) != 1 || threads[0].LastUsed != lastUsed {
				t.Fatalf("completion lost or changed the thread observation: %v, %v", threads, err)
			}
		})
	}
}

func TestExecWithoutThreadFails(t *testing.T) {
	// No event can lead to a catalogue write, so nil is intentionally unused.
	err := ConsumeExec(t.Context(), strings.NewReader("{\"type\":\"future.event\"}\n"), nil, state.Launch{}, io.Discard, io.Discard, "")
	if err == nil || !strings.Contains(err.Error(), "without reporting a thread") {
		t.Fatalf("missing thread = %v", err)
	}
	err = ConsumeExec(t.Context(), strings.NewReader(startedEvent), nil, state.Launch{}, io.Discard, io.Discard, "different-target")
	if err == nil || !strings.Contains(err.Error(), "instead of requested thread") {
		t.Fatalf("a mismatched native UUID was accepted: %v", err)
	}
}
