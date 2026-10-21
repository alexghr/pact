package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alexghr/pact/internal/harness"
	"github.com/coder/websocket"
)

type rpcResponse struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Client connects to a local Codex app-server.
// Call Close to release its connection and reader goroutine.
type Client struct {
	conn    *websocket.Conn
	changes chan struct{}
	done    chan struct{}
	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan rpcResponse
	err     error
}

func Dial(ctx context.Context, socket string) (*Client, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	conn, _, err := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		return nil, fmt.Errorf("connect Codex app-server: %w", err)
	}
	// Thread lists may include long previews; bound each message without the
	// websocket library's small default limit truncating ordinary catalogues.
	conn.SetReadLimit(16 << 20)
	client := &Client{conn: conn, changes: make(chan struct{}, 1), done: make(chan struct{}), pending: make(map[uint64]chan rpcResponse)}
	go client.read()
	if err := client.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "pact", "version": "1"},
	}, nil); err != nil {
		client.Close()
		return nil, err
	}
	// initialize is a request; initialized is its one-way acknowledgment. It
	// has no ID or response; call would incorrectly turn it into a request.
	if err := client.write(ctx, map[string]string{"method": "initialized"}); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

func (c *Client) Close() error {
	err := c.conn.CloseNow()
	<-c.done
	return err
}

// Changes coalesces metadata notifications. Refresh Threads after each hint;
// thread/started alone does not imply a conversation has been saved.
func (c *Client) Changes() <-chan struct{} { return c.changes }
func (c *Client) Done() <-chan struct{}    { return c.done }
func (c *Client) Err() error               { c.mu.Lock(); defer c.mu.Unlock(); return c.err }

func (c *Client) read() {
	defer close(c.done)
	for {
		_, data, err := c.conn.Read(context.Background())
		var response rpcResponse
		if err == nil {
			err = json.Unmarshal(data, &response)
		}
		if err != nil {
			c.mu.Lock()
			c.err = fmt.Errorf("read Codex app-server: %w", err)
			c.mu.Unlock()
			c.conn.CloseNow()
			return
		}
		if response.Method != "" {
			switch response.Method {
			case "thread/started", "thread/name/updated", "thread/archived", "thread/unarchived", "thread/status/changed":
				select {
				case c.changes <- struct{}{}:
				default:
				}
			}
			continue
		}
		c.mu.Lock()
		pending := c.pending[response.ID]
		delete(c.pending, response.ID)
		c.mu.Unlock()
		if pending != nil {
			pending <- response
		}
	}
}

func (c *Client) write(ctx context.Context, message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("write Codex app-server: %w", err)
	}
	return nil
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	response := make(chan rpcResponse, 1)
	c.pending[id] = response
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.write(ctx, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case reply := <-response:
		if reply.Error != nil {
			return fmt.Errorf("Codex %s: %s (%d)", method, reply.Error.Message, reply.Error.Code)
		}
		if result != nil {
			if err := json.Unmarshal(reply.Result, result); err != nil {
				return fmt.Errorf("decode Codex %s: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	}
}

// PrepareResume loads the saved thread with Pact's container permissions before
// the remote TUI rejoins it. Remote TUI resume rejects CLI permission overrides;
// an ID-only resume of this already-loaded thread retains its effective policy.
func (c *Client) PrepareResume(ctx context.Context, threadID string) error {
	var result struct {
		ApprovalPolicy string `json:"approvalPolicy"`
		Sandbox        struct {
			Type string `json:"type"`
		} `json:"sandbox"`
	}
	if err := c.call(ctx, "thread/resume", map[string]any{
		"threadId": threadID, "approvalPolicy": "never", "sandbox": "danger-full-access", "excludeTurns": true,
	}, &result); err != nil {
		return err
	}
	if result.ApprovalPolicy != "never" || result.Sandbox.Type != "dangerFullAccess" {
		return fmt.Errorf("Codex refused Pact's resume permissions: approval policy %q, sandbox %q", result.ApprovalPolicy, result.Sandbox.Type)
	}
	return nil
}

// Threads queries both active and archived saved conversations. Native listing
// owns storage recovery and excludes unsaved thread/started notifications.
func (c *Client) Threads(ctx context.Context) ([]harness.Thread, error) {
	threads := []harness.Thread{}
	for _, archived := range []bool{false, true} {
		cursor := ""
		seen := map[string]bool{}
		for {
			var page struct {
				Data []struct {
					ID        string `json:"id"`
					ParentID  string `json:"forkedFromId"`
					Name      string `json:"name"`
					Preview   string `json:"preview"`
					CreatedAt int64  `json:"createdAt"`
					UpdatedAt int64  `json:"updatedAt"`
				} `json:"data"`
				NextCursor string `json:"nextCursor"`
			}
			params := map[string]any{
				"archived": archived, "limit": 100, "sortKey": "updated_at", "sortDirection": "desc",
				"sourceKinds": []string{"cli", "exec", "appServer", "vscode"}, "modelProviders": []string{},
			}
			if cursor != "" {
				params["cursor"] = cursor
			}
			if err := c.call(ctx, "thread/list", params, &page); err != nil {
				return nil, err
			}
			for _, native := range page.Data {
				if native.ID == "" || native.CreatedAt <= 0 || native.UpdatedAt < native.CreatedAt || native.UpdatedAt > math.MaxInt64/int64(time.Second) {
					return nil, fmt.Errorf("Codex returned invalid metadata for thread %q", native.ID)
				}
				title := native.Name
				if title == "" {
					title = native.Preview
				}
				title = strings.Join(strings.Fields(title), " ")
				if runes := []rune(title); len(runes) > 160 {
					title = string(runes[:160])
				}
				threads = append(threads, harness.Thread{ID: native.ID, ParentID: native.ParentID, Title: title,
					CreatedAt: native.CreatedAt * int64(time.Second), LastUsed: native.UpdatedAt * int64(time.Second), Archived: archived})
			}
			if page.NextCursor == "" {
				break
			}
			if seen[page.NextCursor] {
				return nil, fmt.Errorf("Codex thread/list repeated a pagination cursor")
			}
			seen[page.NextCursor] = true
			cursor = page.NextCursor
		}
	}
	return threads, nil
}
