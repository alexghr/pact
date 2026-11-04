package approval

import (
	"bufio"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Serve prompts for one decision at a time. It owns input and closes it on exit
// or cancellation to interrupt a pending read. The socket's parent must exist.
func Serve(ctx context.Context, socket string, input *os.File, output io.Writer) error {
	defer input.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var inputErr error
	err := serve(ctx, socket, output, func(ctx context.Context, request Request) (bool, error) {
		if err := input.SetReadDeadline(request.ExpiresAt); err != nil {
			return false, fmt.Errorf("set approval input deadline: %w", err)
		}
		readStopped := make(chan struct{})
		stopRead := context.AfterFunc(ctx, func() {
			_ = input.SetReadDeadline(time.Now())
			close(readStopped)
		})
		defer func() {
			// Finish the interrupt before another prompt takes over the input.
			if !stopRead() {
				<-readStopped
			}
			_ = input.SetReadDeadline(time.Time{})
		}()
		// A cancelled scanner retains its error and partial line; each prompt
		// needs a fresh scanner so neither carries over to another request.
		scanner := bufio.NewScanner(input)
		data, err := json.Marshal(request, jsontext.WithIndent("  "))
		if err != nil {
			return false, err
		}
		fmt.Fprintf(output, "\n%s\n", data)
		for {
			fmt.Fprintln(output, "Allow? [y/n]")
			scanned := scanner.Scan()
			if ctx.Err() != nil || !time.Now().Before(request.ExpiresAt) {
				fmt.Fprintln(output, "Request cancelled or expired.")
				return false, context.DeadlineExceeded
			}
			if !scanned {
				inputErr = scanner.Err()
				cancel()
				return false, io.EOF
			}
			switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
			case "y":
				return true, nil
			case "n":
				return false, nil
			}
		}
	})
	return errors.Join(err, inputErr)
}
