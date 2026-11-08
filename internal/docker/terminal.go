package docker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
)

type TerminalSize struct {
	Rows int `json:"rows"`
	Cols int `json:"cols"`
}

// AttachTerminal keeps a TTY container's attachment independent of any host
// terminal. The caller owns stdin and must unblock its reader when the run ends.
func AttachTerminal(ctx context.Context, id string, streams Streams, size TerminalSize, onStart func() error) (bool, error) {
	_, output, input, cleanup, err := engineRequest(ctx,
		"/containers/"+id+"/attach?stream=1&stdin=1&stdout=1&stderr=1", true)
	if err != nil {
		return false, err
	}
	defer cleanup()
	if err := StartContainer(ctx, id); err != nil {
		return false, err
	}
	if err := ResizeTerminal(ctx, id, size); err != nil {
		return true, err
	}
	if err := onStart(); err != nil {
		return true, err
	}
	go func() { _, _ = io.Copy(input, streams.Stdin) }()
	if _, err := io.Copy(streams.Stdout, output); err != nil {
		return true, err
	}
	code, err := outputExitCode(ctx, id)
	if err == nil && code != 0 {
		err = fmt.Errorf("container exited with status %d", code)
	}
	return true, err
}

func ResizeTerminal(ctx context.Context, id string, size TerminalSize) error {
	response, _, _, cleanup, err := engineRequest(ctx,
		fmt.Sprintf("/containers/%s/resize?h=%d&w=%d", id, size.Rows, size.Cols), false)
	if err != nil {
		return err
	}
	defer cleanup()
	return response.Body.Close()
}

// engineRequest uses Docker's connection proxy so context selection and Docker
// Desktop socket discovery stay with the CLI. The attach upgrade then carries
// raw terminal bytes, without the CLI requiring terminal-backed stdin.
func engineRequest(ctx context.Context, path string, upgrade bool) (*http.Response, *bufio.Reader, io.WriteCloser, func(), error) {
	cmd := exec.CommandContext(ctx, "docker", "system", "dial-stdio")
	cmd.Stderr = os.Stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, nil, nil, nil, err
	}
	cleanup := func() {
		_ = input.Close()
		_ = output.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	request, err := http.NewRequest(http.MethodPost, "http://docker"+path, nil)
	if err == nil && upgrade {
		request.Header.Set("Connection", "Upgrade")
		request.Header.Set("Upgrade", "tcp")
	}
	if err == nil {
		err = request.Write(input)
	}
	reader := bufio.NewReader(output)
	var response *http.Response
	if err == nil {
		response, err = http.ReadResponse(reader, request)
	}
	if err != nil {
		cleanup()
		return nil, nil, nil, nil, fmt.Errorf("docker terminal request: %w", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols && response.StatusCode/100 != 2 {
		defer cleanup()
		message, _ := io.ReadAll(response.Body)
		return nil, nil, nil, nil, fmt.Errorf("docker terminal request: %s: %s", response.Status, message)
	}
	return response, reader, input, cleanup, nil
}

func outputExitCode(ctx context.Context, id string) (int, error) {
	code, err := output(ctx, "wait", id)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(code)
}
