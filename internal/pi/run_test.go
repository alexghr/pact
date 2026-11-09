package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/harness"
)

// Exercise the pinned native client, including persistence and extension reloads,
// without credentials or a model service. The endpoint speaks only completion SSE.
func TestPiBindingAndContinuation(t *testing.T) {
	if os.Getenv("PACT_TEST_PI") != "1" {
		t.Skip("set PACT_TEST_PI=1 for the real Pi integration")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	requests := make(chan []byte, 8)
	toolCalls := make(chan string, 4)
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     json.RawMessage
			Method string
			Params struct {
				Name      string
				Arguments map[string]string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "test", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "read_value", "description": "Read a value", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"key": map[string]string{"type": "string"}}, "required": []string{"key"}}}}}
		case "tools/call":
			toolCalls <- request.Params.Name + ":" + request.Params.Arguments["key"]
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "host-tool-result"}}}
		default:
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer mcp.Close()
	requestCount := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		requests <- body
		requestCount++
		w.Header().Set("Content-Type", "text/event-stream")
		if requestCount == 1 {
			arguments, _ := json.Marshal(map[string]string{"code": `text(await tools.mcp__test__read_value({key: "project"}));`})
			chunk := map[string]any{"id": "test", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "read-value", "type": "function", "function": map[string]string{"name": "codemode", "arguments": string(arguments)}}}}, "finish_reason": "tool_calls"}}}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
			return
		}
		fmt.Fprint(w, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Acknowledged.\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", home)
	t.Setenv("NO_PROXY", "127.0.0.1")
	models := fmt.Sprintf(`{"providers":{"local-llama":{"baseUrl":%q,"api":"openai-completions","apiKey":"local","compat":{"supportsDeveloperRole":false,"supportsReasoningEffort":false,"supportsUsageInStreaming":false,"maxTokensField":"max_tokens"},"models":[{"id":"gpt-oss-20b","name":"Local model","reasoning":true,"input":["text"],"contextWindow":49152,"maxTokens":8192,"hideThinkingBlock":true,"samplingParams":{"temperature":1,"top_p":1},"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, provider.URL+"/v1")
	if err := os.WriteFile(filepath.Join(home, "models.json"), []byte(models), 0600); err != nil {
		t.Fatal(err)
	}
	bindings := make(chan string, 4)
	binding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var value harness.Binding
		if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, err := os.Stat(value.BackendRef); err != nil {
			t.Errorf("bound conversation is not durable: %v", err)
		}
		bindings <- value.BackendRef
		w.WriteHeader(http.StatusNoContent)
	}))
	defer binding.Close()
	options := harness.Options{Model: "local-llama/gpt-oss-20b", Effort: "high", MCP: map[string]string{"test": mcp.URL}}
	start := func() *piRPC {
		t.Helper()
		extension, err := writeExtension(t.TempDir(), options, binding.URL)
		if err != nil {
			t.Fatal(err)
		}
		args := []string{"--mode", "rpc", "--model", options.Model, "--thinking", options.Effort, "--tools", "+codemode", "--extension", extension}
		if options.BackendRef != "" {
			args = append(args, "--session", options.BackendRef)
		}
		return startPiRPC(t, ctx, workspace, args)
	}
	client := start()
	client.call(t, "get_state", nil)
	select {
	case ref := <-bindings:
		t.Fatalf("empty session became a resume target: %s", ref)
	default:
	}
	const original = "The project codename is cobalt-otter."
	client.call(t, "prompt", map[string]any{"message": original})
	select {
	case options.BackendRef = <-bindings:
	default:
		t.Fatal("first prompt did not publish a durable conversation")
	}
	readRequest := func() []byte {
		t.Helper()
		select {
		case body := <-requests:
			return body
		case <-ctx.Done():
			t.Fatal("Pi did not contact the custom provider")
			return nil
		}
	}
	request := readRequest()
	if !bytes.Contains(request, []byte(`"model":"gpt-oss-20b"`)) || !bytes.Contains(request, []byte(original)) {
		t.Fatalf("custom provider did not receive the selected model and prompt: %s", request)
	}
	var payload struct {
		Temperature     float64 `json:"temperature"`
		TopP            float64 `json:"top_p"`
		MaxTokens       int     `json:"max_tokens"`
		ReasoningEffort string  `json:"reasoning_effort"`
	}
	if err := json.Unmarshal(request, &payload); err != nil || payload.Temperature != 1 || payload.TopP != 1 || payload.MaxTokens == 0 || payload.ReasoningEffort != "" {
		t.Fatalf("Pi lost local model sampling or compatibility settings: %s, %v", request, err)
	}
	select {
	case call := <-toolCalls:
		if call != "read_value:project" {
			t.Fatalf("MCP call lost its name or arguments: %s", call)
		}
	default:
		t.Fatal("Pi did not call the registered MCP endpoint")
	}
	if result := readRequest(); !bytes.Contains(result, []byte("host-tool-result")) {
		t.Fatalf("MCP result did not reach the custom model: %s", result)
	}
	client.call(t, "new_session", nil)
	client.call(t, "prompt", map[string]any{"message": "A separate native conversation."})
	readRequest()
	select {
	case ref := <-bindings:
		t.Fatalf("native navigation redirected the Pact binding to %s", ref)
	default:
	}
	client.close(t)
	client = start()
	const continuation = "Continue using the project codename."
	client.call(t, "prompt", map[string]any{"message": continuation})
	request = readRequest()
	if !bytes.Contains(request, []byte(original)) || !bytes.Contains(request, []byte(continuation)) || bytes.Contains(request, []byte("separate native conversation")) {
		t.Fatalf("cold resume did not restore the bound conversation: %s", request)
	}
	client.close(t)
	missing := harness.Options{BackendRef: filepath.Join(home, "missing.jsonl")}
	if err := Run(ctx, missing, "", binding.URL); err == nil {
		t.Fatal("missing saved target started a replacement conversation")
	}
}

type piRPC struct {
	command *exec.Cmd
	input   io.WriteCloser
	scanner *bufio.Scanner
	logs    bytes.Buffer
	closed  bool
}

func startPiRPC(t *testing.T, ctx context.Context, workspace string, args []string) *piRPC {
	t.Helper()
	client := &piRPC{command: exec.CommandContext(ctx, "pi", args...)}
	client.command.Dir = workspace
	client.command.Stderr = &client.logs
	var err error
	client.input, err = client.command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := client.command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	client.scanner = bufio.NewScanner(output)
	client.scanner.Buffer(nil, 1<<20)
	if err := client.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.close(t) })
	return client
}

func (c *piRPC) call(t *testing.T, method string, fields map[string]any) {
	t.Helper()
	if fields == nil {
		fields = make(map[string]any)
	}
	fields["type"] = method
	if err := json.NewEncoder(c.input).Encode(fields); err != nil {
		t.Fatal(err)
	}
	response, settled := false, method != "prompt"
	for c.scanner.Scan() {
		var event struct {
			Type, Command, Error string
			Success              bool
		}
		if err := json.Unmarshal(c.scanner.Bytes(), &event); err != nil {
			t.Fatalf("invalid Pi record: %s", c.scanner.Bytes())
		}
		if event.Type == "response" && event.Command == method {
			if !event.Success {
				t.Fatalf("Pi %s: %s", method, event.Error)
			}
			response = true
		}
		if event.Type == "agent_settled" {
			settled = true
		}
		if response && settled {
			return
		}
	}
	t.Fatalf("Pi stopped waiting for %s: %v\n%s", method, c.scanner.Err(), c.logs.String())
}

func (c *piRPC) close(t *testing.T) {
	t.Helper()
	if c.closed {
		return
	}
	c.closed = true
	_ = c.input.Close()
	if err := c.command.Wait(); err != nil && !strings.Contains(err.Error(), "signal: killed") {
		t.Errorf("Pi shutdown: %v\n%s", err, c.logs.String())
	}
}
