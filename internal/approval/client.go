// Package approval sends individual MCP calls to an external approval service.
package approval

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/internal/unixhttp"
)

type Session struct {
	ID          int64  `json:"id"`
	Key         string `json:"key"`
	ExecutionID int64  `json:"executionId"`
	Title       string `json:"title"`
	Workspace   string `json:"workspace"`
	Backend     string `json:"backend"`
}

type ToolCall struct {
	Server    string         `json:"server"`
	Tool      string         `json:"tool"`
	Arguments jsontext.Value `json:"arguments"`
	Rule      string         `json:"rule"`
}

type Request struct {
	ID        string    `json:"id"`
	Hostname  string    `json:"hostname"`
	Session   Session   `json:"session"`
	Call      ToolCall  `json:"call"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type Response struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
}

// Client owns no listener or persistent grants. Each call opens a separate
// connection, allowing a UI to manage concurrent requests and its own policies.
type Client struct {
	http     *http.Client
	hostname string
	session  Session
	Timeout  time.Duration
}

func New(settings config.Approvals, session Session) (*Client, error) {
	if !filepath.IsAbs(settings.Socket) {
		return nil, errors.New("approvals requires an absolute socket path")
	}
	seconds := settings.TimeoutSeconds
	if seconds == 0 {
		seconds = defaultTimeoutSeconds
	}
	if seconds < 1 || seconds > maxTimeoutSeconds {
		return nil, fmt.Errorf("approvals.timeoutSeconds must be between 1 and %d", maxTimeoutSeconds)
	}
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("read approval hostname: %w", err)
	}
	workspace, err := filepath.EvalSymlinks(session.Workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve approval workspace: %w", err)
	}
	outsideWorkspace := func(socket string) error {
		relative, err := filepath.Rel(workspace, socket)
		if err != nil || filepath.IsLocal(relative) {
			return errors.New("approvals.socket must be outside the mounted workspace")
		}
		return nil
	}
	if err := outsideWorkspace(settings.Socket); err != nil {
		return nil, err
	}
	transport := &http.Transport{
		DisableKeepAlives: true,
		// Recheck the resolved path when SSH replaces its forwarded socket.
		DialContext: unixhttp.Dialer(settings.Socket, outsideWorkspace),
	}
	return &Client{
		http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		hostname: hostname, session: session, Timeout: time.Duration(seconds) * time.Second,
	}, nil
}

// Approve waits for a decision for precisely this invocation. Unavailable peers,
// invalid replies, deadlines, and cancellation never authorize execution.
func (c *Client) Approve(ctx context.Context, call ToolCall) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	expiresAt, _ := ctx.Deadline()
	request := Request{
		ID: rand.Text(), Hostname: c.hostname, Session: c.session, Call: call,
		CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt.UTC(),
	}
	var body bytes.Buffer
	if err := json.MarshalWrite(&body, request); err != nil {
		return false, fmt.Errorf("encode approval request: %w", err)
	}
	if body.Len() > maxRequestBytes {
		return false, fmt.Errorf("approval request exceeds %d bytes", maxRequestBytes)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://pact/v1/approvals", &body)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("request approval: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("approval service returned HTTP %d", response.StatusCode)
	}
	// Read one extra byte to distinguish an oversized response from an exact fit.
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return false, fmt.Errorf("read approval: %w", err)
	}
	var decision Response
	if len(data) > maxResponseBytes || json.Unmarshal(data, &decision) != nil || decision.ID != request.ID {
		return false, errors.New("invalid approval response")
	}
	switch decision.Decision {
	case "approve":
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return true, nil
	case "reject":
		return false, nil
	default:
		return false, errors.New("unknown approval decision")
	}
}
