package artifacts

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/state"
)

func TestBrokerSocketLifetime(t *testing.T) {
	broker, err := StartBroker(context.Background(), openTestStore(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := broker.Close(); err != nil {
			t.Error(err)
		}
	})
	directory, err := os.Stat(broker.directory)
	if err != nil {
		t.Fatal(err)
	}
	socket, err := os.Stat(broker.socket)
	if err != nil {
		t.Fatal(err)
	}
	if directory.Mode().Perm() != 0700 || socket.Mode().Perm() != 0600 || socket.Mode()&os.ModeSocket == 0 {
		t.Fatalf("broker permissions: directory %v, socket %v", directory.Mode(), socket.Mode())
	}
	connection, err := net.Dial("unix", broker.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	// Closing must also work when a client has not finished initializing.
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(broker.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("broker directory after Close: %v", err)
	}
	if conn, err := net.Dial("unix", broker.socket); err == nil {
		conn.Close()
		t.Fatal("closed broker accepted a connection")
	}
}

func TestMessageReaderBoundsFrames(t *testing.T) {
	const limit = 1024
	frame := `"` + strings.Repeat("x", limit-3) + "\"\n"
	for _, tt := range []struct {
		name, input string
		valid       bool
	}{
		{"maximum frame", frame, true},
		{"separate frames exceeding total limit", strings.Repeat(frame, 3), true},
		{"oversized frame", `"` + strings.Repeat("x", limit) + "\"\n", false},
		{"unterminated oversized frame", `"` + strings.Repeat("x", limit), false},
		{"JSON split across lines", "{\n\"padding\": \"x\"\n}\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reader := newMessageReader(io.NopCloser(strings.NewReader(tt.input)), limit)
			output, err := io.ReadAll(reader)
			if tt.valid {
				if err != nil || string(output) != tt.input {
					t.Fatalf("read returned %q, %v", output, err)
				}
			} else if err == nil || len(output) != 0 {
				t.Fatalf("invalid frame reached decoder: %d bytes, %v", len(output), err)
			}
		})
	}
}

func TestBrokerConcurrentClientsAndConnectionLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	broker, err := StartBroker(ctx, openTestStore(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := broker.Close(); err != nil {
			t.Error(err)
		}
	})
	var clients []net.Conn
	for range maxBrokerConnections {
		conn := dialBroker(t, broker)
		brokerRequest(t, conn, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
		clients = append(clients, conn)
	}
	for _, conn := range clients {
		brokerRequest(t, conn, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	}
	excess := dialBroker(t, broker)
	assertConnectionClosed(t, excess)

	// Parent cancellation must interrupt idle clients and Accept, without
	// requiring the clients to disconnect or a caller to close the listener.
	cancel()
	for _, conn := range clients {
		assertConnectionClosed(t, conn)
	}
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerRejectsOversizedMessage(t *testing.T) {
	store := openTestStore(t)
	service := newTestService(t, store)
	artifact, err := service.CreateArtifact(t.Context(), CreateArtifactInput{Name: "Transport limits"})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := StartBroker(context.Background(), store, service.pactSessionID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := broker.Close(); err != nil {
			t.Error(err)
		}
	})
	allowed := dialBroker(t, broker)
	brokerRequest(t, allowed, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	// The wire limit must still permit a maximum-size base64-encoded file.
	fmt.Fprintf(allowed, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"write_artifact_file","arguments":{"artifact_id":%d,"path":"large.bin","expected_version":0,"encoding":"base64","content":"`, artifact.ID)
	encoder := base64.NewEncoder(base64.StdEncoding, allowed)
	if _, err := io.CopyN(encoder, &repeatedByteReader{}, state.MaxArtifactFileBytes); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(allowed, `"}}}`)
	brokerResponse(t, allowed)
	stored, err := store.GetArtifact(t.Context(), artifact.ID)
	if err != nil || len(stored.Files) != 1 || stored.Files[0].SizeBytes != state.MaxArtifactFileBytes {
		t.Fatalf("maximum-size file was not saved: %#v, %v", stored, err)
	}
	conn := dialBroker(t, broker)
	// Never complete the JSON object: the broker must enforce the limit while
	// receiving it, before JSON validation or any tool handler can run.
	_, _ = io.Copy(conn, io.MultiReader(
		strings.NewReader(`{"padding":"`),
		io.LimitReader(&repeatedByteReader{}, maxBrokerMessageBytes),
	))
	assertConnectionClosed(t, conn)
	// A bad client must not stop the listener from serving another client.
	healthy := dialBroker(t, broker)
	brokerRequest(t, healthy, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
}

type repeatedByteReader struct{}

func (*repeatedByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func dialBroker(t *testing.T, broker *Broker) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", broker.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn
}

func brokerRequest(t *testing.T, conn net.Conn, request string) {
	t.Helper()
	if _, err := fmt.Fprintln(conn, request); err != nil {
		t.Fatal(err)
	}
	brokerResponse(t, conn)
}

func brokerResponse(t *testing.T, conn net.Conn) {
	t.Helper()
	reply, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(reply, &response); err != nil || response.Result == nil || response.Error != nil {
		t.Fatalf("MCP response = %s, error = %v", reply, err)
	}
}

func assertConnectionClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	var p [1]byte
	_, err := conn.Read(p[:])
	if timeout, ok := err.(net.Error); err == nil || (ok && timeout.Timeout()) {
		t.Fatalf("connection was not closed: %v", err)
	}
}
