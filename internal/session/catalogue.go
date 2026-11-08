package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexghr/pact/internal/state"
)

// OpenStore prepares Pact's host storage and opens its catalogue.
func OpenStore(ctx context.Context, path string) (*state.Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}
	return state.Open(ctx, path)
}

//func smth() {
//	dir := os.Getenv("XDG_STATE_HOME")
//	if dir == "" {
//		home, err := os.UserHomeDir()
//		if err != nil {
//			return nil, fmt.Errorf("find catalogue home: %w", err)
//		}
//		dir = filepath.Join(home, ".local", "state")
//	}
//	if !filepath.IsAbs(dir) {
//		return nil, errors.New("catalogue state directory must be absolute")
//	}
//}
