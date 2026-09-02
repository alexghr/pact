package docker

import "context"

type Engine interface {
	Build(context.Context, BuildOptions) error
	Run(context.Context, RunOptions) error
}

type BuildOptions struct {
	ContextDir string
	Dockerfile string
	Tag        string
}

type RunOptions struct {
	Image   string
	Args    []string
	Env     []string
	Volumes []string
}
