package docker

import (
	"context"
	"io"
)

type IOHandler func(context.Context, io.Reader, io.Writer) error

type ImageBuilder interface {
	Build(context.Context, BuildOptions) error
	ImageExists(context.Context, string) (bool, error)
}

type ContainerRunner interface {
	Run(context.Context, RunOptions, IOHandler) error
}

type Engine interface {
	ImageBuilder
	ContainerRunner
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
