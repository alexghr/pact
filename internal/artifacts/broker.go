package artifacts

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alexghr/pact/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const ContainerSocketPath = "/opt/pact/artifacts.sock"

// Allow a maximum-size file encoded as base64 plus protocol overhead.
const maxBrokerMessageBytes = 32 << 20
// One proxy connection plus room for a replacement during a restart.
const maxBrokerConnections = 2

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
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(ctx, func() { _ = b.listener.Close() })
	defer stop()
	var clients sync.WaitGroup
	defer clients.Wait()
	defer cancel()
	slots := make(chan struct{}, maxBrokerConnections)
	for {
		connection, err := b.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return ctx.Err()
			}
			return fmt.Errorf("accept artifact MCP connection: %w", err)
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = connection.Close()
			continue
		}
		clients.Go(func() {
			defer func() { <-slots }()
			conn := &onceCloseConnection{Conn: connection}
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
			defer stop()
			_ = NewServer(store, pactSessionID).Run(ctx, &mcp.IOTransport{
				Reader: newMessageReader(conn, maxBrokerMessageBytes),
				Writer: conn,
			})
		})
	}
}

// Frame and bound NDJSON before the SDK's JSON decoder can buffer it. Validate
// each line so incomplete JSON cannot accumulate across otherwise bounded lines.
type messageReader struct {
	io.Closer
	scanner *bufio.Scanner
	pending []byte
}

func newMessageReader(conn io.ReadCloser, limit int) *messageReader {
	// default read line by line
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, min(4096, limit)), limit)
	return &messageReader{Closer: conn, scanner: scanner}
}

func (r *messageReader) Read(p []byte) (int, error) {
	// the target buffer is empty, noop
	if len(p) == 0 {
		return 0, nil
	}
	// try to read a 'token'
	if len(r.pending) == 0 {
		if !r.scanner.Scan() {
			if err := r.scanner.Err(); err != nil {
				return 0, fmt.Errorf("read artifact MCP message: %w", err)
			}
			return 0, io.EOF
		}
		// read a whole line
		line := r.scanner.Bytes()
		if !json.Valid(line) {
			return 0, errors.New("artifact MCP message must be valid JSON on one line")
		}
		// keep it in pending in case len(p) < len(line) so multiple calls to Read don't advance the scanner every time
		r.pending = append(line, '\n')
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
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
