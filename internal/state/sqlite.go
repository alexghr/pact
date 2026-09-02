package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS runs (
	id INTEGER PRIMARY KEY,
	workspace_dir TEXT NOT NULL,
	model TEXT NOT NULL,
	effort TEXT NOT NULL,
	dockerfile_variant TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('running', 'finished', 'error')),
	exit_code INTEGER,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	completed_at TEXT
);`

type Store struct {
	db *sql.DB
}

type Run struct {
	WorkspaceDir      string
	Model             string
	Effort            string
	DockerfileVariant string
}

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)

	for _, statement := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		schema,
		"PRAGMA user_version = 1",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize database: %w", err)
		}
	}

	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, fmt.Errorf("set database permissions: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) StartRun(ctx context.Context, run Run) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO runs (
			workspace_dir,
			model,
			effort,
			dockerfile_variant,
			status
		) VALUES (?, ?, ?, ?, 'running')`,
		run.WorkspaceDir,
		run.Model,
		run.Effort,
		run.DockerfileVariant,
	)
	if err != nil {
		return 0, fmt.Errorf("insert run: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read run id: %w", err)
	}
	return id, nil
}

func (s *Store) CompleteRun(ctx context.Context, id int64, status string, exitCode *int) error {
	if status != "finished" && status != "error" {
		return fmt.Errorf("complete run: invalid status %q", status)
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE runs
		SET status = ?, exit_code = ?,
			completed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ? AND status = 'running'`,
		status,
		exitCode,
		id,
	)
	if err != nil {
		return fmt.Errorf("complete run: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("complete run: read affected rows: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("complete run: running row %d not found", id)
	}
	return nil
}
