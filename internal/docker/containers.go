package docker

import (
	"context"
	"encoding/csv"
	"fmt"
	"strings"
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
