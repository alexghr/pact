package docker

import (
	"reflect"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	options := BuildOptions{
		ContextDir: "./container",
		Dockerfile: "container/Dockerfile.go",
		Tag:        "pact-codex:go",
	}
	want := []string{
		"build",
		"--tag", "pact-codex:go",
		"--file", "container/Dockerfile.go",
		"./container",
	}

	if got := buildArgs(options); !reflect.DeepEqual(got, want) {
		t.Fatalf("buildArgs() = %#v, want %#v", got, want)
	}
}

func TestRunArgs(t *testing.T) {
	options := RunOptions{
		Image: "pact-codex:generic",
		Args:  []string{"run", "write tests", "gpt-5.6-sol", "low"},
		Env:   []string{"HOST_UID=1000", "HOST_GID=1000"},
		Volumes: []string{
			"/tmp/project:/home/pact/workspace",
			"pact-codex-state:/home/pact/.codex",
		},
	}
	want := []string{
		"run", "--rm",
		"--volume", "/tmp/project:/home/pact/workspace",
		"--volume", "pact-codex-state:/home/pact/.codex",
		"--env", "HOST_UID=1000",
		"--env", "HOST_GID=1000",
		"pact-codex:generic",
		"run", "write tests", "gpt-5.6-sol", "low",
	}

	if got := runArgs(options); !reflect.DeepEqual(got, want) {
		t.Fatalf("runArgs() = %#v, want %#v", got, want)
	}
}
