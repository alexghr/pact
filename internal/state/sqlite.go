// Package state stores Pact's host-side catalogue independently of agent history.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

// Open creates or opens the host catalogue and applies pending migrations.
func Open(ctx context.Context) (*Store, error) {
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

	options := url.Values{
		"_journal_mode": {"WAL"},
		"_foreign_keys": {"on"},
		"_busy_timeout": {"5000"},
	}
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     filepath.Join(dir, "sessions.db"),
		RawQuery: options.Encode(),
	}).String()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open catalogue: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(ctx, migrations); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate catalogue: %w", err)
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
