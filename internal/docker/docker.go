package docker

import (
	"context"
	"io"
)

type IOHandler func(context.Context, io.Reader, io.Writer) error

type Engine interface {
	Build(context.Context, BuildOptions) error
	Run(context.Context, RunOptions, IOHandler) error
}

type BuildOptions struct {
	ContextDir string
	Dockerfile string
	Tag        string
}

type RunOptions struct {
	Image   string
	Env     []string
	Volumes []string
}
