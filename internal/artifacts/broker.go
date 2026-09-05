package artifacts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alexghr/pact/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const ContainerSocketPath = "/opt/pact/artifacts.sock"

type Broker struct {
	directory string
	socket    string
	listener  net.Listener
	cancel    context.CancelFunc
	done      chan error
	closeOnce sync.Once
	closeErr  error
}

func StartBroker(ctx context.Context, store *state.Store, pactSessionID int64) (*Broker, error) {
	directory, err := os.MkdirTemp("", fmt.Sprintf("pact-artifacts-%d-", pactSessionID))
	if err != nil {
		return nil, fmt.Errorf("create artifact broker directory: %w", err)
	}
	keepDirectory := false
	defer func() {
		if !keepDirectory {
			_ = os.RemoveAll(directory)
		}
	}()

	socket := filepath.Join(directory, "mcp.sock")
	if strings.Contains(socket, ":") {
		return nil, errors.New("artifact broker socket path contains ':' and cannot be mounted safely")
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("listen for artifact MCP: %w", err)
	}
	if err := os.Chmod(socket, 0600); err != nil {
		listener.Close()
		return nil, fmt.Errorf("set artifact MCP socket permissions: %w", err)
	}

	brokerContext, cancel := context.WithCancel(ctx)
	broker := &Broker{
		directory: directory,
		socket:    socket,
		listener:  listener,
		cancel:    cancel,
		done:      make(chan error, 1),
	}
	go func() {
		broker.done <- broker.serve(brokerContext, store, pactSessionID)
	}()
	keepDirectory = true
	return broker, nil
}

func (b *Broker) Mount() string {
	return b.socket + ":" + ContainerSocketPath + ":ro"
}

func (b *Broker) Close() error {
	b.closeOnce.Do(func() {
		b.cancel()
		listenerErr := b.listener.Close()
		serveErr := <-b.done
		removeErr := os.RemoveAll(b.directory)
		if errors.Is(listenerErr, net.ErrClosed) {
			listenerErr = nil
		}
		if errors.Is(serveErr, context.Canceled) || errors.Is(serveErr, net.ErrClosed) {
			serveErr = nil
		}
		b.closeErr = errors.Join(listenerErr, serveErr, removeErr)
	})
	return b.closeErr
}

func (b *Broker) serve(ctx context.Context, store *state.Store, pactSessionID int64) error {
	for {
		connection, err := b.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return ctx.Err()
			}
			return fmt.Errorf("accept artifact MCP connection: %w", err)
		}
		conn := &onceCloseConnection{Conn: connection}
		err = NewServer(store, pactSessionID).Run(ctx, &mcp.IOTransport{
			Reader: conn,
			Writer: conn,
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			continue
		}
	}
}

type onceCloseConnection struct {
	net.Conn
	once sync.Once
}

func (c *onceCloseConnection) Close() error {
	var err error
	c.once.Do(func() { err = c.Conn.Close() })
	return err
}
