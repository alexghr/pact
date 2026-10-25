package docker

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type ContainerConfig struct {
	Name        string
	Image       string
	Network     string
	Restart     string
	Entrypoint  string
	User        string
	Command     []string
	Env         []string
	Labels      map[string]string
	Mounts      []Mount
	Privileged  bool
	TTY         bool
	Interactive bool
	Remove      bool
	Detach      bool
}

func CreateContainer(ctx context.Context, config ContainerConfig) (string, error) {
	return output(ctx, append([]string{"create"}, config.args()...)...)
}

func RunContainer(ctx context.Context, config ContainerConfig, streams Streams) error {
	return run(ctx, streams, append([]string{"run"}, config.args()...)...)
}

func StartContainer(ctx context.Context, id string) error {
	_, err := output(ctx, "start", id)
	return err
}

func RemoveContainer(ctx context.Context, id string, force, volumes bool) error {
	args := []string{"rm"}
	if force {
		args = append(args, "--force")
	}
	if volumes {
		args = append(args, "--volumes")
	}
	_, err := output(ctx, append(args, id)...)
	return err
}

func ContainersUsingVolume(ctx context.Context, volume string) ([]string, error) {
	ids, err := output(ctx, "container", "ls", "--all", "--filter", "volume="+volume, "--format", "{{.ID}}")
	return strings.Fields(ids), err
}

func Exec(ctx context.Context, id string, command []string, streams Streams) error {
	args := []string{"exec"}
	if streams.Stdin != nil {
		args = append(args, "--interactive")
	}
	return run(ctx, streams, append(append(args, id), command...)...)
}

func ExecOutput(ctx context.Context, id string, command ...string) (string, error) {
	return output(ctx, append([]string{"exec", id}, command...)...)
}

func Logs(ctx context.Context, id string, tail int, streams Streams) error {
	args := []string{"logs"}
	if tail > 0 {
		args = append(args, "--tail", fmt.Sprint(tail))
	}
	return run(ctx, streams, append(args, id)...)
}

func CopyTo(ctx context.Context, source, id, target string) error {
	_, err := output(ctx, "cp", source, id+":"+target)
	return err
}

type AttachOptions struct {
	Streams
	Interactive, Interrupt bool
	OnStart                func() error
	Consume                func(io.Reader) error
}

// Attach owns the attached Docker client and its output pipe. The caller owns
// the container and must remove it even if cancellation ends only the client.
func Attach(ctx context.Context, id string, options AttachOptions) (started bool, err error) {
	args := []string{"start", "--attach"}
	if options.Interactive {
		args = append(args, "--interactive")
	}
	cmd := exec.CommandContext(ctx, "docker", append(args, id)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = options.Stdin, options.Stdout, options.Stderr
	if options.Interrupt {
		cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	}
	cmd.WaitDelay = 5 * time.Second
	var events, writer *os.File
	if options.Consume != nil {
		var err error
		events, writer, err = os.Pipe()
		if err != nil {
			return false, err
		}
		defer events.Close()
		defer writer.Close()
		cmd.Stdout = writer
	}
	err = cmd.Start()
	if writer != nil {
		// The child has its own descriptor; our copy must not delay EOF.
		_ = writer.Close()
	}
	started = err == nil
	if started {
		if options.OnStart != nil {
			err = options.OnStart()
		}
		if err == nil && options.Consume != nil {
			err = options.Consume(events)
		}
		if err != nil {
			_ = cmd.Process.Kill()
		}
		err = errors.Join(err, cmd.Wait())
	}
	// Preserve Docker's reserved startup-failure exit codes.
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() >= 125 && exitErr.ExitCode() <= 127 {
		started = false
	}
	return started, err
}

type Mount struct {
	Type, Source, Target, Subpath string
	ReadOnly, NoCopy              bool
}

func (c ContainerConfig) args() []string {
	var args []string
	for _, option := range [][2]string{
		{"--name", c.Name}, {"--network", c.Network}, {"--restart", c.Restart},
		{"--entrypoint", c.Entrypoint}, {"--user", c.User},
	} {
		if option[1] != "" {
			args = append(args, option[:]...)
		}
	}
	for _, option := range []struct {
		enabled bool
		flag    string
	}{
		{c.Privileged, "--privileged"}, {c.TTY, "--tty"}, {c.Interactive, "--interactive"},
		{c.Remove, "--rm"}, {c.Detach, "--detach"},
	} {
		if option.enabled {
			args = append(args, option.flag)
		}
	}
	args = append(args, labelsArgs(c.Labels)...)
	for _, mount := range c.Mounts {
		fields := []string{"type=" + mount.Type, "source=" + mount.Source, "target=" + mount.Target}
		if mount.ReadOnly {
			fields = append(fields, "readonly")
		}
		if mount.NoCopy {
			fields = append(fields, "volume-nocopy")
		}
		if mount.Subpath != "" {
			fields = append(fields, "volume-subpath="+mount.Subpath)
		}
		var value strings.Builder
		writer := csv.NewWriter(&value)
		_ = writer.Write(fields)
		writer.Flush()
		args = append(args, "--mount", strings.TrimSuffix(value.String(), "\n"))
	}
	for _, env := range c.Env {
		args = append(args, "--env", env)
	}
	return append(append(args, c.Image), c.Command...)
}
