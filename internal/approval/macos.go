package approval

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

//go:embed dialog.js
var dialogScript string

type buttonName string

const (
	approveButton      buttonName = "approve"
	approveToolButton  buttonName = "approveTool"
	approveGroupButton buttonName = "approveGroup"
	denyButton         buttonName = "deny"
)

type grant struct {
	hostname, session, server, tool string
}

type grants map[grant]bool

func (g grants) allows(r Request) bool {
	key := grant{r.Hostname, r.Session.Key, r.Call.Server, r.Call.Tool}
	if g[key] {
		return true
	}

	key.tool = "" // An empty tool grants the entire server.
	return g[key]
}

func (g grants) remember(r Request, group bool) {
	key := grant{r.Hostname, r.Session.Key, r.Call.Server, r.Call.Tool}
	if group {
		key.tool = ""
	}
	g[key] = true
}

func ServeMacOS(ctx context.Context, socket string, output io.Writer) error {
	rules := make(grants)

	return serve(ctx, socket, output, func(ctx context.Context, request Request) (bool, error) {
		if request.Session.Key == "" || request.Call.Server == "" || request.Call.Tool == "" {
			return false, errors.New("approval request needs a session key, server, and tool")
		}

		if rules.allows(request) {
			return true, nil
		}

		action, err := showDialog(ctx, request)
		if err != nil {
			return false, err
		}

		if err := ctx.Err(); err != nil {
			return false, err
		}

		if !time.Now().Before(request.ExpiresAt) {
			return false, context.DeadlineExceeded
		}

		switch action {
		case approveButton:
			return true, nil
		case approveToolButton, approveGroupButton:
			rules.remember(request, action == approveGroupButton)
			return true, nil
		case denyButton:
			return false, nil
		default:
			return false, fmt.Errorf("unexpected dialog action %q", action)
		}
	})
}

type button struct {
	Name  buttonName `json:"name"`
	Title string     `json:"title"`
}

type model struct {
	Summary string   `json:"summary"`
	Details string   `json:"details"`
	Buttons []button `json:"buttons"`
}

func showDialog(ctx context.Context, request Request) (buttonName, error) {
	details, err := json.Marshal(request, jsontext.WithIndent("  "))
	if err != nil {
		return "", err
	}

	display := model{
		Summary: fmt.Sprintf("%s · Session %d: %s\n%s/%s\nExpires %s",
			request.Hostname, request.Session.ID, request.Session.Title,
			request.Call.Server, request.Call.Tool, request.ExpiresAt.Local().Format(time.TimeOnly)),

		Details: string(details),
		Buttons: []button{
			{approveButton, "Approve"},
			{approveToolButton, fmt.Sprintf("Approve %s/%s for session %d", request.Call.Server, request.Call.Tool, request.Session.ID)},
			{approveGroupButton, fmt.Sprintf("Approve %s/* for session %d", request.Call.Server, request.Session.ID)},
			{denyButton, "Deny"},
		},
	}

	data, err := json.Marshal(display)
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", dialogScript)
	cmd.Stdin = bytes.NewReader(data)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	result, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("show approval dialog: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	return buttonName(strings.TrimSpace(string(result))), nil
}
