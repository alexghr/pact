package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"net"
	"net/http"
	"sync"
)

type rpcResponse struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Client connects to a local Codex app-server.
// Call Close to release its connection and reader goroutine.
type Client struct {
	conn     *websocket.Conn
	bindings chan string
	done     chan struct{}
	mu       sync.Mutex
	nextID   uint64
	pending  map[uint64]chan rpcResponse
	err      error
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
	client := &Client{conn: conn, bindings: make(chan string, 1), done: make(chan struct{}), pending: make(map[uint64]chan rpcResponse)}
	go client.read()
	if err := client.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "pact", "version": "1"},
	}, nil); err != nil {
		readErr := client.Err()
		client.Close()
		return nil, fmt.Errorf("initialize Codex: %w (reader: %v)", err, readErr)
	}
	// initialize is a request; initialized is its one-way acknowledgment. It
	// has no ID or response; call would incorrectly turn it into a request.
	if err := client.write(ctx, map[string]string{"method": "initialized"}); err != nil {
		readErr := client.Err()
		client.Close()
		return nil, fmt.Errorf("initialize Codex: %w (reader: %v)", err, readErr)
	}
	return client, nil
}

func (c *Client) Close() error {
	err := c.conn.CloseNow()
	<-c.done
	return err
}

// Bindings emits the initial conversation after its first accepted user turn.
func (c *Client) Bindings() <-chan string { return c.bindings }
func (c *Client) Done() <-chan struct{}   { return c.done }
func (c *Client) Err() error              { c.mu.Lock(); defer c.mu.Unlock(); return c.err }

func (c *Client) read() {
	defer close(c.done)
	bound := false
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
		if !bound && response.Method == "thread/status/changed" {
			var params struct {
				ThreadID string `json:"threadId"`
				Status   struct {
					Type string `json:"type"`
				} `json:"status"`
			}
			if err := json.Unmarshal(response.Params, &params); err != nil {
				c.mu.Lock()
				c.err = fmt.Errorf("read Codex status: %w", err)
				c.mu.Unlock()
				c.conn.CloseNow()
				return
			}
			if params.Status.Type == "active" && params.ThreadID != "" {
				c.bindings <- params.ThreadID
				bound = true
			}
		}
		if response.Method != "" {
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
