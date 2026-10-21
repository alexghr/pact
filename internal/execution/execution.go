// Package execution owns foreground writers and their recoverable Docker resources.
package execution

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/alexghr/pact/internal/state"
)

const resourceLabel = "me.alexghr.pact.resource"

type Owner struct {
	Store  *state.Store
	Launch state.Launch
}

func docker(ctx context.Context, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func (o *Owner) reserve(ctx context.Context, kind, prefix, image string) (state.Resource, []string, error) {
	r := state.Resource{Name: prefix + strings.ToLower(rand.Text()), Kind: kind, Image: image}
	labels := []string{
		"--label", resourceLabel + "=" + r.Name,
		"--label", "me.alexghr.pact.environment=" + o.Launch.Environment.ID,
		"--label", "me.alexghr.pact.workspace=" + strconv.FormatInt(o.Launch.Environment.WorkspaceID, 10),
		"--label", "me.alexghr.pact.session=" + strconv.FormatInt(o.Launch.SessionID, 10),
		"--label", "me.alexghr.pact.execution=" + strconv.FormatInt(o.Launch.ExecutionID, 10),
	}
	return r, labels, o.Store.ReserveResource(ctx, o.Launch.ExecutionID, r)
}

// CreateContainer records intent first, then the actual Docker ID and image.
// A crash between creation and bookkeeping is recovered using the reserved name.
func (o *Owner) CreateContainer(ctx context.Context, prefix, image string, args []string) (state.Resource, error) {
	r, labels, err := o.reserve(ctx, "container", prefix, image)
	if err != nil {
		return r, err
	}
	create := append([]string{"create", "--name", r.Name}, labels...)
	if _, err := docker(ctx, append(create, args...)...); err != nil {
		return r, err
	}
	info, err := inspect(ctx, r)
	if err != nil {
		return r, err
	}
	if info == nil {
		return r, fmt.Errorf("created container %s disappeared before start", r.Name)
	}
	r.ContainerID, r.ImageID = info.ID, info.Image
	return r, o.Store.ResourceCreated(ctx, r.Name, r.ContainerID, r.ImageID)
}

func (o *Owner) CreateVolume(ctx context.Context) (state.Resource, error) {
	r, labels, err := o.reserve(ctx, "volume", "pact-dind-", "")
	if err != nil {
		return r, err
	}
	_, err = docker(ctx, append(append([]string{"volume", "create"}, labels...), r.Name)...)
	return r, err
}

type resourceInfo struct {
	ID, Image string
	Labels    map[string]string
	Config    struct{ Labels map[string]string }
	State     struct {
		Running, Paused, Restarting bool
	}
}

// Listing distinguishes absence from a daemon failure. Inspection then verifies
// ownership and immutable identity before any removal.
func inspect(ctx context.Context, r state.Resource) (*resourceInfo, error) {
	args := []string{r.Kind, "ls", "--filter", "name=" + r.Name, "--format", "{{.Name}}"}
	if r.Kind == "container" {
		args = []string{"container", "ls", "--all", "--filter", "name=" + r.Name, "--format", "{{.Names}}"}
	}
	names, err := docker(ctx, args...)
	if err != nil {
		return nil, err
	}
	if !strings.Contains("\n"+names+"\n", "\n"+r.Name+"\n") {
		return nil, nil
	}
	output, err := docker(ctx, r.Kind, "inspect", r.Name)
	if err != nil {
		return nil, err
	}
	var info []resourceInfo
	if err := json.Unmarshal([]byte(output), &info); err != nil {
		return nil, err
	}
	if len(info) != 1 {
		return nil, fmt.Errorf("unexpected Docker inspection for %s", r.Name)
	}
	labels := info[0].Labels
	if r.Kind == "container" {
		labels = info[0].Config.Labels
	}
	if labels[resourceLabel] != r.Name || (r.ContainerID != "" && info[0].ID != r.ContainerID) {
		return nil, fmt.Errorf("refusing changed or unowned Docker %s %s", r.Kind, r.Name)
	}
	return &info[0], nil
}

func (o *Owner) Remove(ctx context.Context, r state.Resource) error {
	info, err := inspect(ctx, r)
	if err != nil {
		return err
	}
	if info != nil {
		args := []string{"volume", "rm", r.Name}
		if r.Kind == "container" {
			args = []string{"rm", "--force", "--volumes", info.ID}
		}
		if _, err := docker(ctx, args...); err != nil {
			return err
		}
	}
	return o.Store.ResourceCleaned(ctx, r.Name)
}

// Cleanup runs before releasing execution ownership, even after cancellation.
func (o *Owner) Cleanup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resources, err := o.Store.Resources(ctx, o.Launch.ExecutionID)
	if err != nil {
		return err
	}
	for _, resource := range resources {
		err = errors.Join(err, o.Remove(ctx, resource))
	}
	return err
}

// Recover never stops a surviving writer or replays an input. The operator must
// stop a running resource explicitly, then retry the requested launch/resume.
func (o *Owner) Recover(ctx context.Context) error {
	ids, err := o.Store.PendingExecutions(ctx, o.Launch)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := o.recoverExecution(ctx, id); err != nil {
			message := fmt.Sprintf("cannot recover execution %d: %v; verify Docker is reachable, stop/remove the reported resources, then retry explicitly", id, err)
			return errors.Join(errors.New(message), o.Store.UncertainExecution(ctx, id, message))
		}
		if err := o.Store.FinishExecution(ctx, id, state.Interrupted, 1, "launcher ended without completing cleanup; recorded resources reconciled; input was not replayed"); err != nil {
			return err
		}
	}
	return nil
}

func (o *Owner) recoverExecution(ctx context.Context, id int64) error {
	resources, err := o.Store.Resources(ctx, id)
	if err != nil {
		return err
	}
	for _, r := range resources {
		info, err := inspect(ctx, r)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", r.Name, err)
		}
		if info != nil && (info.State.Running || info.State.Paused || info.State.Restarting) {
			return fmt.Errorf("previous writer may still exist in %s; stop it with docker rm --force %s", r.Name, r.Name)
		}
	}
	for _, r := range resources {
		if err := o.Remove(ctx, r); err != nil {
			return err
		}
	}
	return nil
}
