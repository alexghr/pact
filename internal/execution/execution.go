// Package execution owns foreground writers and their recoverable Docker resources.
package execution

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/state"
)

const resourceLabel = "me.alexghr.pact.resource"

type Owner struct {
	Store  *state.Store
	Launch state.Launch
}

func (o *Owner) reserve(ctx context.Context, kind, prefix, image string) (state.Resource, map[string]string, error) {
	r := state.Resource{Name: prefix + strings.ToLower(rand.Text()), Kind: kind, Image: image}
	labels := map[string]string{
		resourceLabel:                 r.Name,
		"me.alexghr.pact.environment": o.Launch.Environment.ID,
		"me.alexghr.pact.workspace":   strconv.FormatInt(o.Launch.Environment.WorkspaceID, 10),
		"me.alexghr.pact.session":     strconv.FormatInt(o.Launch.SessionID, 10),
		"me.alexghr.pact.execution":   strconv.FormatInt(o.Launch.ExecutionID, 10),
	}
	return r, labels, o.Store.ReserveResource(ctx, o.Launch.ExecutionID, r)
}

// CreateContainer records intent first, then the actual Docker ID and image.
// A crash between creation and bookkeeping is recovered using the reserved name.
func (o *Owner) CreateContainer(ctx context.Context, prefix string, config docker.ContainerConfig) (state.Resource, error) {
	r, labels, err := o.reserve(ctx, "container", prefix, config.Image)
	if err != nil {
		return r, err
	}
	config.Name = r.Name
	config.Labels = maps.Clone(config.Labels)
	if config.Labels == nil {
		config.Labels = make(map[string]string)
	}
	for key, value := range labels {
		config.Labels[key] = value
	}
	if _, err := docker.CreateContainer(ctx, config); err != nil {
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
	err = docker.CreateVolume(ctx, r.Name, labels)
	return r, err
}

// Listing distinguishes absence from a daemon failure. Inspection then verifies
// ownership and immutable identity before any removal.
func inspect(ctx context.Context, r state.Resource) (*docker.Info, error) {
	info, err := docker.Inspect(ctx, r.Kind, r.Name)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, nil
	}
	if info.Labels[resourceLabel] != r.Name || (r.ContainerID != "" && info.ID != r.ContainerID) {
		return nil, fmt.Errorf("refusing changed or unowned Docker %s %s", r.Kind, r.Name)
	}
	return info, nil
}

func (o *Owner) Remove(ctx context.Context, r state.Resource) error {
	info, err := inspect(ctx, r)
	if err != nil {
		return err
	}
	if info != nil {
		var err error
		if r.Kind == "container" {
			err = docker.RemoveContainer(ctx, info.ID, true, true)
		} else {
			err = docker.RemoveVolume(ctx, r.Name)
		}
		if err != nil {
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
