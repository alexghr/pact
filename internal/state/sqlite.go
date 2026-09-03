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
);

CREATE TABLE IF NOT EXISTS codex_sessions (
	run_id INTEGER PRIMARY KEY,
	thread_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	user_agent TEXT NOT NULL,
	state_volume TEXT NOT NULL,
	transcript_json TEXT,
	transcript_captured_at TEXT,
	FOREIGN KEY (run_id) REFERENCES runs(id)
);

CREATE TABLE IF NOT EXISTS codex_events (
	run_id INTEGER NOT NULL,
	sequence INTEGER NOT NULL,
	method TEXT NOT NULL,
	params_json TEXT NOT NULL,
	received_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (run_id, sequence),
	FOREIGN KEY (run_id) REFERENCES runs(id)
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

type RunRecord struct {
	ID                int64
	WorkspaceDir      string
	Model             string
	Effort            string
	DockerfileVariant string
	Status            string
}

type CodexSession struct {
	ThreadID    string
	SessionID   string
	UserAgent   string
	StateVolume string
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
		"PRAGMA foreign_keys = ON",
		schema,
		"PRAGMA user_version = 2",
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

func (s *Store) ListRuns(ctx context.Context) ([]RunRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, workspace_dir, model, effort, dockerfile_variant, status
		FROM runs
		ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()

	var runs []RunRecord
	for rows.Next() {
		var run RunRecord
		if err := rows.Scan(
			&run.ID,
			&run.WorkspaceDir,
			&run.Model,
			&run.Effort,
			&run.DockerfileVariant,
			&run.Status,
		); err != nil {
			return nil, fmt.Errorf("list runs: scan row: %w", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runs: read rows: %w", err)
	}
	return runs, nil
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

func (s *Store) StartCodexSession(ctx context.Context, runID int64, session CodexSession) error {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO codex_sessions (
			run_id,
			thread_id,
			session_id,
			user_agent,
			state_volume
		)
		SELECT ?, ?, ?, ?, ?
		WHERE EXISTS (
			SELECT 1 FROM runs WHERE id = ? AND status = 'running'
		)`,
		runID,
		session.ThreadID,
		session.SessionID,
		session.UserAgent,
		session.StateVolume,
		runID,
	)
	if err != nil {
		return fmt.Errorf("insert Codex session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("insert Codex session: read affected rows: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("insert Codex session: running row %d not found", runID)
	}
	return nil
}

func (s *Store) AppendCodexEvent(
	ctx context.Context,
	runID int64,
	sequence int64,
	method string,
	params []byte,
) error {
	paramsJSON := "null"
	if len(params) != 0 {
		paramsJSON = string(params)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO codex_events (run_id, sequence, method, params_json)
		VALUES (?, ?, ?, ?)`,
		runID,
		sequence,
		method,
		paramsJSON,
	); err != nil {
		return fmt.Errorf("insert Codex event: %w", err)
	}
	return nil
}

func (s *Store) StoreCodexTranscript(ctx context.Context, runID int64, transcript []byte) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE codex_sessions
		SET transcript_json = ?,
			transcript_captured_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE run_id = ?`,
		string(transcript),
		runID,
	)
	if err != nil {
		return fmt.Errorf("store Codex transcript: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store Codex transcript: read affected rows: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("store Codex transcript: session for run %d not found", runID)
	}
	return nil
}
