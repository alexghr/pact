package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

type Client struct {
	decoder *json.Decoder
	encoder *json.Encoder

	writeMu sync.Mutex
	stateMu sync.Mutex
	nextID  int64
	pending map[int64]chan Message
	// The Codex server can send notifications or approval requests via JSON-RPC. This ring buffer stores them.
	messages messageBuffer
	readErr  error
	ready    chan struct{}
	done     chan struct{}
}

type Message struct {
	Method string          `json:"method,omitempty"`
	ID     json.RawMessage `json:"id,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("app-server error %d: %s", e.Code, e.Message)
}

type ClientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

type InitializeResult struct {
	UserAgent      string `json:"userAgent"`
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOS     string `json:"platformOs"`
}

type ApprovalPolicy string

const ApprovalNever ApprovalPolicy = "never"

type ThreadOptions struct {
	Model          string         `json:"model,omitempty"`
	CWD            string         `json:"cwd,omitempty"`
	ApprovalPolicy ApprovalPolicy `json:"approvalPolicy,omitempty"`
	ServiceName    string         `json:"serviceName,omitempty"`
}

type ThreadResumeOptions struct {
	Model          string         `json:"model,omitempty"`
	CWD            string         `json:"cwd,omitempty"`
	ApprovalPolicy ApprovalPolicy `json:"approvalPolicy,omitempty"`
}

type Thread struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
}

type SandboxType string

const SandboxExternal SandboxType = "externalSandbox"

type NetworkAccess string

const NetworkEnabled NetworkAccess = "enabled"

type SandboxPolicy struct {
	Type          SandboxType   `json:"type"`
	NetworkAccess NetworkAccess `json:"networkAccess,omitempty"`
}

type TurnOptions struct {
	Model          string         `json:"model,omitempty"`
	Effort         string         `json:"effort,omitempty"`
	CWD            string         `json:"cwd,omitempty"`
	ApprovalPolicy ApprovalPolicy `json:"approvalPolicy,omitempty"`
	SandboxPolicy  *SandboxPolicy `json:"sandboxPolicy,omitempty"`
}

type Turn struct {
	ID     string     `json:"id"`
	Status string     `json:"status"`
	Error  *TurnError `json:"error,omitempty"`
}

type TurnError struct {
	Message string `json:"message"`
}

func NewClient(reader io.Reader, writer io.Writer) *Client {
	c := &Client{
		decoder: json.NewDecoder(reader),
		encoder: json.NewEncoder(writer),
		nextID:  1,
		pending: make(map[int64]chan Message),
		ready:   make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	go c.read()
	return c
}

func (c *Client) Initialize(ctx context.Context, info ClientInfo) (InitializeResult, error) {
	var result InitializeResult
	if err := c.call(ctx, "initialize", struct {
		ClientInfo ClientInfo `json:"clientInfo"`
	}{ClientInfo: info}, &result); err != nil {
		return InitializeResult{}, err
	}
	if err := c.notify(ctx, "initialized", struct{}{}); err != nil {
		return InitializeResult{}, err
	}
	return result, nil
}

func (c *Client) StartThread(ctx context.Context, options ThreadOptions) (Thread, error) {
	var result struct {
		Thread Thread `json:"thread"`
	}
	if err := c.call(ctx, "thread/start", options, &result); err != nil {
		return Thread{}, err
	}
	if result.Thread.ID == "" {
		return Thread{}, errors.New("thread/start: response did not include a thread id")
	}
	return result.Thread, nil
}

func (c *Client) ResumeThread(ctx context.Context, threadID string, options ThreadResumeOptions) (Thread, error) {
	params := struct {
		ThreadID string `json:"threadId"`
		ThreadResumeOptions
	}{ThreadID: threadID, ThreadResumeOptions: options}

	var result struct {
		Thread Thread `json:"thread"`
	}
	if err := c.call(ctx, "thread/resume", params, &result); err != nil {
		return Thread{}, err
	}
	if result.Thread.ID == "" {
		return Thread{}, errors.New("thread/resume: response did not include a thread id")
	}
	if result.Thread.ID != threadID {
		return Thread{}, fmt.Errorf("thread/resume: response returned thread %q, want %q", result.Thread.ID, threadID)
	}
	return result.Thread, nil
}

func (c *Client) StartTurn(ctx context.Context, threadID, prompt string, options TurnOptions) (Turn, error) {
	params := struct {
		ThreadID string `json:"threadId"`
		Input    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"input"`
		TurnOptions
	}{
		ThreadID: threadID,
		Input: []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: prompt}},
		TurnOptions: options,
	}

	var result struct {
		Turn Turn `json:"turn"`
	}
	if err := c.call(ctx, "turn/start", params, &result); err != nil {
		return Turn{}, err
	}
	if result.Turn.ID == "" {
		return Turn{}, errors.New("turn/start: response did not include a turn id")
	}
	return result.Turn, nil
}

func (c *Client) ReadThread(ctx context.Context, threadID string) (json.RawMessage, error) {
	var result json.RawMessage
	if err := c.call(ctx, "thread/read", struct {
		ThreadID     string `json:"threadId"`
		IncludeTurns bool   `json:"includeTurns"`
	}{ThreadID: threadID, IncludeTurns: true}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) NextMessage(ctx context.Context) (Message, error) {
	for {
		c.stateMu.Lock()
		if message, ok := c.messages.pop(); ok {
			c.stateMu.Unlock()
			return message, nil
		}
		readErr := c.readErr
		c.stateMu.Unlock()

		if readErr != nil {
			return Message{}, readErr
		}
		select {
		case <-ctx.Done():
			return Message{}, ctx.Err()
		case <-c.ready:
		case <-c.done:
		}
	}
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	response := make(chan Message, 1)

	// do this inside a function to deal with mutexes nicely
	id, err := (func() (int64, error) {
		c.stateMu.Lock()
		defer c.stateMu.Unlock()

		if c.readErr != nil {
			err := c.readErr
			return 0, fmt.Errorf("%s: %w", method, err)
		}
		id := c.nextID
		c.nextID++
		c.pending[id] = response
		return id, nil
	}())

	if err != nil {
		return err
	}

	request := struct {
		Method string `json:"method"`
		ID     int64  `json:"id"`
		Params any    `json:"params"`
	}{Method: method, ID: id, Params: params}
	if err := c.write(ctx, request); err != nil {
		c.removePending(id)
		return fmt.Errorf("%s: %w", method, err)
	}

	var message Message
	select {
	case message = <-response:
	case <-ctx.Done():
		c.removePending(id)
		return fmt.Errorf("%s: %w", method, ctx.Err())
	case <-c.done:
		select {
		case message = <-response:
		default:
			return fmt.Errorf("%s: %w", method, c.readerError())
		}
	}

	if message.Error != nil {
		return fmt.Errorf("%s: %w", method, message.Error)
	}
	if len(message.Result) == 0 {
		return fmt.Errorf("%s: response did not include a result", method)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(message.Result, result); err != nil {
		return fmt.Errorf("%s: decode result: %w", method, err)
	}
	return nil
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	notification := struct {
		Method string `json:"method"`
		Params any    `json:"params"`
	}{Method: method, Params: params}
	if err := c.write(ctx, notification); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	return nil
}

func (c *Client) write(ctx context.Context, message any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.encoder.Encode(message); err != nil {
		return fmt.Errorf("write app-server message: %w", err)
	}
	return nil
}

func (c *Client) read() {
	for {
		var message Message
		if err := c.decoder.Decode(&message); err != nil {
			if errors.Is(err, io.EOF) {
				err = errors.New("app-server closed the connection")
			} else {
				err = fmt.Errorf("read app-server message: %w", err)
			}
			c.stateMu.Lock()
			c.readErr = err
			c.stateMu.Unlock()
			close(c.done)
			return
		}

		if message.Method == "" {
			var id int64
			if err := json.Unmarshal(message.ID, &id); err == nil {
				c.stateMu.Lock()
				response := c.pending[id]
				delete(c.pending, id)
				c.stateMu.Unlock()
				if response != nil {
					response <- message
					continue
				}
			}
		}

		c.stateMu.Lock()
		if !c.messages.push(message) {
			c.readErr = errors.New("app-server message buffer is full")
			c.stateMu.Unlock()
			close(c.done)
			return
		}
		c.stateMu.Unlock()
		select {
		case c.ready <- struct{}{}:
		default:
		}
	}
}

func (c *Client) removePending(id int64) {
	c.stateMu.Lock()
	delete(c.pending, id)
	c.stateMu.Unlock()
}

func (c *Client) readerError() error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.readErr
}
