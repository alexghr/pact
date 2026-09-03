package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestClientStartsPrompt(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	t.Cleanup(func() {
		clientReader.Close()
		clientWriter.Close()
		serverReader.Close()
		serverWriter.Close()
	})

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveStartPrompt(serverReader, serverWriter)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := NewClient(clientReader, clientWriter)
	initialized, err := client.Initialize(ctx, ClientInfo{
		Name:    "pact",
		Title:   "Pact",
		Version: "0.1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if initialized.UserAgent != "codex/0.149.0" || initialized.PlatformOS != "linux" {
		t.Fatalf("Initialize() = %#v", initialized)
	}

	thread, err := client.StartThread(ctx, ThreadOptions{
		Model:          "gpt-5.6-sol",
		CWD:            "/home/pact/workspace",
		ApprovalPolicy: ApprovalNever,
		ServiceName:    "pact",
	})
	if err != nil {
		t.Fatal(err)
	}
	if thread.ID != "thr_123" || thread.SessionID != "thr_123" {
		t.Fatalf("StartThread() = %#v", thread)
	}

	turn, err := client.StartTurn(ctx, thread.ID, "fix the tests", TurnOptions{
		Model:          "gpt-5.6-sol",
		Effort:         "low",
		CWD:            "/home/pact/workspace",
		ApprovalPolicy: ApprovalNever,
		SandboxPolicy: &SandboxPolicy{
			Type:          SandboxExternal,
			NetworkAccess: NetworkEnabled,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if turn.ID != "turn_456" || turn.Status != "inProgress" {
		t.Fatalf("StartTurn() = %#v", turn)
	}

	message, err := client.NextMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if message.Method != "thread/started" {
		t.Fatalf("NextMessage() method = %q, want thread/started", message.Method)
	}

	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestClientReturnsRPCError(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	t.Cleanup(func() {
		clientReader.Close()
		clientWriter.Close()
		serverReader.Close()
		serverWriter.Close()
	})

	go func() {
		decoder := json.NewDecoder(serverReader)
		encoder := json.NewEncoder(serverWriter)
		var request wireMessage
		if err := decoder.Decode(&request); err != nil {
			return
		}
		encoder.Encode(map[string]any{
			"id": request.ID,
			"error": map[string]any{
				"code":    -32600,
				"message": "Not initialized",
			},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := NewClient(clientReader, clientWriter).Initialize(ctx, ClientInfo{Name: "pact"})
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Initialize() error = %v, want RPCError", err)
	}
	if rpcErr.Code != -32600 || rpcErr.Message != "Not initialized" {
		t.Fatalf("RPCError = %#v", rpcErr)
	}
}

func TestMessageBufferWrapsAndRejectsOverflow(t *testing.T) {
	var buffer messageBuffer
	for i := range messageBufferSize {
		if !buffer.push(Message{Method: fmt.Sprintf("message-%d", i)}) {
			t.Fatalf("push %d failed before buffer was full", i)
		}
	}
	if buffer.push(Message{Method: "overflow"}) {
		t.Fatal("push succeeded when buffer was full")
	}

	message, ok := buffer.pop()
	if !ok || message.Method != "message-0" {
		t.Fatalf("first pop = %#v, %t", message, ok)
	}
	if !buffer.push(Message{Method: "wrapped"}) {
		t.Fatal("push after pop failed")
	}

	for i := 1; i < messageBufferSize; i++ {
		message, ok := buffer.pop()
		if !ok || message.Method != fmt.Sprintf("message-%d", i) {
			t.Fatalf("pop %d = %#v, %t", i, message, ok)
		}
	}
	message, ok = buffer.pop()
	if !ok || message.Method != "wrapped" {
		t.Fatalf("wrapped pop = %#v, %t", message, ok)
	}
	if _, ok := buffer.pop(); ok {
		t.Fatal("pop succeeded when buffer was empty")
	}
}

type wireMessage struct {
	Method  string          `json:"method"`
	ID      int64           `json:"id"`
	Params  json.RawMessage `json:"params"`
	JSONRPC string          `json:"jsonrpc"`
}

func serveStartPrompt(reader io.Reader, writer io.Writer) error {
	decoder := json.NewDecoder(reader)
	encoder := json.NewEncoder(writer)

	initialize, err := readWireMessage(decoder, "initialize", 1)
	if err != nil {
		return err
	}
	var initializeParams struct {
		ClientInfo ClientInfo `json:"clientInfo"`
	}
	if err := json.Unmarshal(initialize.Params, &initializeParams); err != nil {
		return err
	}
	if initializeParams.ClientInfo.Name != "pact" || initializeParams.ClientInfo.Title != "Pact" ||
		initializeParams.ClientInfo.Version != "0.1.0" {
		return fmt.Errorf("initialize params = %#v", initializeParams)
	}
	if err := encoder.Encode(map[string]any{
		"id": 1,
		"result": map[string]any{
			"userAgent":      "codex/0.149.0",
			"codexHome":      "/home/pact/.codex",
			"platformFamily": "unix",
			"platformOs":     "linux",
		},
	}); err != nil {
		return err
	}

	if _, err := readWireMessage(decoder, "initialized", 0); err != nil {
		return err
	}

	startThread, err := readWireMessage(decoder, "thread/start", 2)
	if err != nil {
		return err
	}
	var threadOptions ThreadOptions
	if err := json.Unmarshal(startThread.Params, &threadOptions); err != nil {
		return err
	}
	if threadOptions.Model != "gpt-5.6-sol" || threadOptions.CWD != "/home/pact/workspace" ||
		threadOptions.ApprovalPolicy != ApprovalNever || threadOptions.ServiceName != "pact" {
		return fmt.Errorf("thread/start params = %#v", threadOptions)
	}
	if err := encoder.Encode(map[string]any{
		"method": "thread/started",
		"params": map[string]any{"thread": map[string]any{"id": "thr_123"}},
	}); err != nil {
		return err
	}
	if err := encoder.Encode(map[string]any{
		"id": 2,
		"result": map[string]any{
			"thread": map[string]any{"id": "thr_123", "sessionId": "thr_123"},
		},
	}); err != nil {
		return err
	}

	startTurn, err := readWireMessage(decoder, "turn/start", 3)
	if err != nil {
		return err
	}
	var turnParams struct {
		ThreadID string `json:"threadId"`
		Input    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"input"`
		TurnOptions
	}
	if err := json.Unmarshal(startTurn.Params, &turnParams); err != nil {
		return err
	}
	if turnParams.ThreadID != "thr_123" || len(turnParams.Input) != 1 ||
		turnParams.Input[0].Type != "text" || turnParams.Input[0].Text != "fix the tests" {
		return fmt.Errorf("turn/start input = %#v", turnParams)
	}
	if turnParams.Model != "gpt-5.6-sol" || turnParams.Effort != "low" ||
		turnParams.CWD != "/home/pact/workspace" || turnParams.ApprovalPolicy != ApprovalNever ||
		turnParams.SandboxPolicy == nil || turnParams.SandboxPolicy.Type != SandboxExternal ||
		turnParams.SandboxPolicy.NetworkAccess != NetworkEnabled {
		return fmt.Errorf("turn/start options = %#v", turnParams.TurnOptions)
	}

	return encoder.Encode(map[string]any{
		"id": 3,
		"result": map[string]any{
			"turn": map[string]any{"id": "turn_456", "status": "inProgress"},
		},
	})
}

func readWireMessage(decoder *json.Decoder, method string, id int64) (wireMessage, error) {
	var message wireMessage
	if err := decoder.Decode(&message); err != nil {
		return wireMessage{}, err
	}
	if message.Method != method || message.ID != id {
		return wireMessage{}, fmt.Errorf("message = %#v, want method %q id %d", message, method, id)
	}
	if message.JSONRPC != "" {
		return wireMessage{}, fmt.Errorf("message includes unsupported jsonrpc header %q", message.JSONRPC)
	}
	return message, nil
}
