// Package codex reads the native CLI protocol without storing conversation text.
package codex

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type execEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Message  string `json:"message"`
	Error    struct {
		Message string `json:"message"`
	} `json:"error"`
	Item struct {
		Type             string `json:"type"`
		Text             string `json:"text"`
		Command          string `json:"command"`
		AggregatedOutput string `json:"aggregated_output"`
		ExitCode         int    `json:"exit_code"`
		Server           string `json:"server"`
		Tool             string `json:"tool"`
		Query            string `json:"query"`
		Changes          []struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		} `json:"changes"`
	} `json:"item"`
}

// ConsumeExec consumes the one prompted run's stdout. Diagnostics are a separate
// process stream; only assistant messages are rendered to output. A captured
// thread survives a later protocol error, process failure, or cancellation.
func ConsumeExec(input io.Reader, bind func(string) error, output, diagnostics io.Writer, expectedThread string) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	threadID := ""
	completed := false
	var reportedError error
	for scanner.Scan() {
		var event execEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return fmt.Errorf("read Codex event: %w", err)
		}
		switch event.Type {
		case "thread.started":
			if expectedThread != "" && event.ThreadID != expectedThread {
				return fmt.Errorf("Codex resumed thread %q instead of requested thread %q", event.ThreadID, expectedThread)
			}
			if threadID != "" {
				if threadID != event.ThreadID {
					return fmt.Errorf("Codex changed thread during a prompted run")
				}
				continue
			}
			threadID = event.ThreadID
			if err := bind(threadID); err != nil {
				return err
			}
		case "turn.started", "turn.completed":
			if threadID == "" {
				return fmt.Errorf("Codex reported a turn before a thread")
			}
			completed = event.Type == "turn.completed"
		case "turn.failed":
			return fmt.Errorf("Codex turn failed: %s", event.Error.Message)
		case "error":
			// Codex remains running after an error notification; the turn outcome
			// decides whether it recovered. Retain the diagnostic if the stream ends.
			reportedError = fmt.Errorf("Codex error: %s", event.Message)
			if _, err := fmt.Fprintln(diagnostics, reportedError); err != nil {
				return err
			}
		case "item.started", "item.updated", "item.completed":
			if threadID == "" {
				return fmt.Errorf("Codex reported an item before a thread")
			}
			if err := renderItem(event, output, diagnostics); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read Codex events: %w", err)
	}
	if threadID == "" {
		return errors.Join(fmt.Errorf("Codex exited without reporting a thread"), reportedError)
	}
	if !completed {
		return errors.Join(fmt.Errorf("Codex exited without completing the turn"), reportedError)
	}
	return nil
}

func renderItem(event execEvent, output, diagnostics io.Writer) error {
	item := event.Item
	var err error
	switch item.Type {
	case "agent_message":
		if event.Type == "item.completed" {
			_, err = fmt.Fprintln(output, item.Text)
		}
	case "reasoning":
		if event.Type == "item.completed" {
			_, err = fmt.Fprintln(diagnostics, item.Text)
		}
	case "command_execution":
		if event.Type == "item.started" {
			_, err = fmt.Fprintf(diagnostics, "> %s\n", item.Command)
		} else if event.Type == "item.completed" {
			if item.AggregatedOutput != "" {
				_, err = fmt.Fprintln(diagnostics, item.AggregatedOutput)
			}
			if err == nil && item.ExitCode != 0 {
				_, err = fmt.Fprintf(diagnostics, "Command exited with status %d\n", item.ExitCode)
			}
		}
	case "mcp_tool_call":
		if event.Type == "item.started" {
			_, err = fmt.Fprintf(diagnostics, "> %s/%s\n", item.Server, item.Tool)
		}
	case "web_search":
		if event.Type == "item.completed" {
			_, err = fmt.Fprintf(diagnostics, "Search: %s\n", item.Query)
		}
	case "file_change":
		if event.Type == "item.completed" {
			for _, change := range item.Changes {
				if _, err := fmt.Fprintf(diagnostics, "%s %s\n", change.Kind, change.Path); err != nil {
					return err
				}
			}
		}
	}
	return err
}
