// Package state stores Pact's host-side catalogue independently of agent history.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db   *sql.DB
	path string
}

// Open creates or opens the host catalogue and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("catalogue path is required")
	}
	options := url.Values{
		"_journal_mode": {"WAL"},
		"_foreign_keys": {"on"},
		"_busy_timeout": {"5000"},
	}
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: options.Encode(),
	}).String()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open catalogue: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, path: path}
	if err := store.migrate(ctx, migrations); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate catalogue: %w", err)
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Path is the database location supplied by the caller.
func (s *Store) Path() string { return s.path }
