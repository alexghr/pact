package artifacts

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
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
