package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexghr/pact/internal/state"
)

// OpenStore prepares Pact's host storage and opens its catalogue.
func OpenStore(ctx context.Context) (*state.Store, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("find catalogue home: %w", err)
		}
		dir = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(dir) {
		return nil, errors.New("catalogue state directory must be absolute")
	}
	dir = filepath.Join(dir, "pact")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create catalogue directory: %w", err)
	}
	return state.Open(ctx, filepath.Join(dir, "sessions.db"))
}
