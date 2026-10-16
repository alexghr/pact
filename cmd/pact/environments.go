package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/alexghr/pact/internal/environment"
	"github.com/alexghr/pact/internal/state"
)

func manageEnvironments(args []string) (runErr error) {
	gc := len(args) == 1 && args[0] == "gc"
	var sessionID int64
	if len(args) == 2 && args[0] == "repair" {
		sessionID, _ = strconv.ParseInt(args[1], 10, 64)
	}
	if !gc && sessionID <= 0 {
		return errors.New("usage: pact environments gc | pact environments repair SESSION_ID")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := state.Open(ctx)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, store.Close()) }()
	if gc {
		removed, err := environment.Collect(ctx, store)
		for _, id := range removed {
			fmt.Fprintln(os.Stdout, "Removed environment", id)
		}
		if err == nil && len(removed) == 0 {
			fmt.Fprintln(os.Stdout, "No unreferenced environments to remove.")
		}
		return err
	}
	unlock, err := store.LockStorage(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	env, err := store.SessionEnvironment(ctx, sessionID)
	if err != nil {
		return err
	}
	if err := environment.Prepare(ctx, store, env); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Prepared environment %s for session %d\n", env.ID, sessionID)
	return nil
}
