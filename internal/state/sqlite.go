package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS pact_sessions (
	id INTEGER PRIMARY KEY,
	workspace_dir TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS codex_threads (
	thread_id TEXT PRIMARY KEY,
	pact_session_id INTEGER NOT NULL,
	session_id TEXT NOT NULL,
	state_volume TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	FOREIGN KEY (pact_session_id) REFERENCES pact_sessions(id)
);

CREATE TABLE IF NOT EXISTS runs (
	id INTEGER PRIMARY KEY,
	pact_session_id INTEGER NOT NULL,
	thread_id TEXT,
	user_agent TEXT,
	model TEXT NOT NULL,
	effort TEXT NOT NULL,
	dockerfile_variant TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('running', 'finished', 'error')),
	exit_code INTEGER,
	transcript_json TEXT,
	transcript_captured_at TEXT,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	completed_at TEXT,
	FOREIGN KEY (pact_session_id) REFERENCES pact_sessions(id),
	FOREIGN KEY (thread_id) REFERENCES codex_threads(thread_id)
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

const sessionSelect = `
	WITH session_view AS (
		SELECT session.id, session.workspace_dir, session.created_at,
			(
				SELECT MAX(latest_run.id)
				FROM runs AS latest_run
				WHERE latest_run.pact_session_id = session.id
			) AS latest_run_id,
			(
				SELECT latest_thread.thread_id
				FROM runs AS latest_thread
				WHERE latest_thread.pact_session_id = session.id
					AND latest_thread.thread_id IS NOT NULL
				ORDER BY latest_thread.id DESC
				LIMIT 1
			) AS thread_id
		FROM pact_sessions AS session
	)
	SELECT session.id, COALESCE(run.id, 0), session.workspace_dir,
		COALESCE(run.model, ''), COALESCE(run.effort, ''),
		COALESCE(run.dockerfile_variant, ''), COALESCE(run.status, 'pending'),
		session.created_at,
		COALESCE(run.completed_at, run.created_at, session.created_at),
		COALESCE(session.thread_id, ''), COALESCE(thread.session_id, ''),
		COALESCE((
			SELECT transcript_run.transcript_json
			FROM runs AS transcript_run
			WHERE transcript_run.pact_session_id = session.id
				AND transcript_run.thread_id = session.thread_id
				AND transcript_run.transcript_json IS NOT NULL
			ORDER BY transcript_run.id DESC
			LIMIT 1
		), '')
	FROM session_view AS session
	LEFT JOIN runs AS run ON run.id = session.latest_run_id
	LEFT JOIN codex_threads AS thread ON thread.thread_id = session.thread_id`

var (
	ErrSessionNotFound      = errors.New("session not found")
	ErrResumeTargetNotFound = errors.New("resume target not found")
)

type Store struct {
	db *sql.DB
}

type Run struct {
	PactSessionID     int64
	Model             string
	Effort            string
	DockerfileVariant string
}

type RunRecord struct {
	ID                int64
	PactSessionID     int64
	WorkspaceDir      string
	Model             string
	Effort            string
	DockerfileVariant string
	Status            string
}

type CodexThread struct {
	ThreadID    string
	SessionID   string
	UserAgent   string
	StateVolume string
}

type SessionRecord struct {
	ID                int64
	LatestRunID       int64
	WorkspaceDir      string
	Model             string
	Effort            string
	DockerfileVariant string
	Status            string
	CreatedAt         string
	UpdatedAt         string
	ThreadID          string
	CodexSessionID    string
	LastAgentMessage  string
	TranscriptJSON    json.RawMessage
}

type CodexEventRecord struct {
	RunID      int64
	Sequence   int64
	Method     string
	ParamsJSON json.RawMessage
	ReceivedAt string
}

type ResumeTarget struct {
	PactSessionID     int64
	RunID             int64
	WorkspaceDir      string
	Model             string
	Effort            string
	DockerfileVariant string
	ThreadID          string
	SessionID         string
	StateVolume       string
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

func (s *Store) CreateSession(ctx context.Context, workspace string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO pact_sessions (workspace_dir)
		VALUES (?)`, workspace)
	if err != nil {
		return 0, fmt.Errorf("create session: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read session id: %w", err)
	}
	return id, nil
}

func (s *Store) StartRun(ctx context.Context, run Run) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO runs (
			pact_session_id,
			model,
			effort,
			dockerfile_variant,
			status
		)
		SELECT ?, ?, ?, ?, 'running'
		WHERE EXISTS (SELECT 1 FROM pact_sessions WHERE id = ?)`,
		run.PactSessionID,
		run.Model,
		run.Effort,
		run.DockerfileVariant,
		run.PactSessionID,
	)
	if err != nil {
		return 0, fmt.Errorf("insert run: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("insert run: read affected rows: %w", err)
	}
	if rows != 1 {
		return 0, fmt.Errorf("insert run: session %d not found", run.PactSessionID)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read run id: %w", err)
	}
	return id, nil
}

func (s *Store) ListRuns(ctx context.Context) ([]RunRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run.id, run.pact_session_id, session.workspace_dir,
			run.model, run.effort, run.dockerfile_variant,
			run.status
		FROM runs AS run
		JOIN pact_sessions AS session ON session.id = run.pact_session_id
		ORDER BY run.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()

	var runs []RunRecord
	for rows.Next() {
		var run RunRecord
		if err := rows.Scan(
			&run.ID,
			&run.PactSessionID,
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

func (s *Store) ListSessions(ctx context.Context) ([]SessionRecord, error) {
	rows, err := s.db.QueryContext(ctx, sessionSelect+" ORDER BY session.id DESC")
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionRecord
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("list sessions: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions: read rows: %w", err)
	}
	return sessions, nil
}

func (s *Store) GetSession(ctx context.Context, sessionID int64) (SessionRecord, error) {
	session, err := scanSession(s.db.QueryRowContext(ctx, sessionSelect+" WHERE session.id = ?", sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, fmt.Errorf("get session %d: %w", sessionID, ErrSessionNotFound)
	}
	if err != nil {
		return SessionRecord{}, fmt.Errorf("get session %d: %w", sessionID, err)
	}
	return session, nil
}

type scanner interface {
	Scan(...any) error
}

func scanSession(row scanner) (SessionRecord, error) {
	var session SessionRecord
	var transcript string
	if err := row.Scan(
		&session.ID,
		&session.LatestRunID,
		&session.WorkspaceDir,
		&session.Model,
		&session.Effort,
		&session.DockerfileVariant,
		&session.Status,
		&session.CreatedAt,
		&session.UpdatedAt,
		&session.ThreadID,
		&session.CodexSessionID,
		&transcript,
	); err != nil {
		return SessionRecord{}, err
	}
	if transcript != "" {
		session.TranscriptJSON = json.RawMessage(transcript)
		lastMessage, err := lastAgentMessage(transcript)
		if err != nil {
			return SessionRecord{}, fmt.Errorf("decode transcript: %w", err)
		}
		session.LastAgentMessage = lastMessage
	}
	return session, nil
}

func (s *Store) ListSessionEvents(ctx context.Context, sessionID int64) ([]CodexEventRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT event.run_id, event.sequence, event.method,
			event.params_json, event.received_at
		FROM codex_events AS event
		JOIN runs AS run ON run.id = event.run_id
		WHERE run.pact_session_id = ?
		ORDER BY event.run_id, event.sequence`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list events for session %d: %w", sessionID, err)
	}
	defer rows.Close()

	var events []CodexEventRecord
	for rows.Next() {
		var event CodexEventRecord
		var params string
		if err := rows.Scan(
			&event.RunID,
			&event.Sequence,
			&event.Method,
			&params,
			&event.ReceivedAt,
		); err != nil {
			return nil, fmt.Errorf("list events for session %d: scan row: %w", sessionID, err)
		}
		event.ParamsJSON = json.RawMessage(params)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events for session %d: read rows: %w", sessionID, err)
	}
	return events, nil
}

func (s *Store) GetResumeTarget(ctx context.Context, sessionID int64) (ResumeTarget, error) {
	var target ResumeTarget
	err := s.db.QueryRowContext(ctx, `
		SELECT session.id, run.id, session.workspace_dir,
			run.model, run.effort, run.dockerfile_variant,
			run.thread_id, thread.session_id, thread.state_volume
		FROM pact_sessions AS session
		JOIN runs AS run ON run.id = (
			SELECT latest.id
			FROM runs AS latest
			WHERE latest.pact_session_id = session.id
				AND latest.thread_id IS NOT NULL
			ORDER BY latest.id DESC
			LIMIT 1
		)
		JOIN codex_threads AS thread ON thread.thread_id = run.thread_id
		WHERE session.id = ?`, sessionID).Scan(
		&target.PactSessionID,
		&target.RunID,
		&target.WorkspaceDir,
		&target.Model,
		&target.Effort,
		&target.DockerfileVariant,
		&target.ThreadID,
		&target.SessionID,
		&target.StateVolume,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ResumeTarget{}, fmt.Errorf("resume session %d: %w", sessionID, ErrResumeTargetNotFound)
	}
	if err != nil {
		return ResumeTarget{}, fmt.Errorf("resume session %d: %w", sessionID, err)
	}
	return target, nil
}

func lastAgentMessage(transcript string) (string, error) {
	var result struct {
		Thread struct {
			Turns []struct {
				Items []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"items"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if err := json.Unmarshal([]byte(transcript), &result); err != nil {
		return "", err
	}
	if len(result.Thread.Turns) == 0 {
		return "", nil
	}
	items := result.Thread.Turns[len(result.Thread.Turns)-1].Items
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Type == "agentMessage" && items[i].Text != "" {
			return items[i].Text, nil
		}
	}
	return "", nil
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

func (s *Store) LinkCodexThread(ctx context.Context, runID int64, thread CodexThread) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("link Codex thread: begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO codex_threads (thread_id, pact_session_id, session_id, state_volume)
		SELECT ?, run.pact_session_id, ?, ?
		FROM runs AS run
		WHERE run.id = ? AND run.status = 'running'
		ON CONFLICT(thread_id) DO NOTHING`,
		thread.ThreadID,
		thread.SessionID,
		thread.StateVolume,
		runID,
	); err != nil {
		return fmt.Errorf("link Codex thread: insert thread: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE codex_threads
		SET session_id = ?
		WHERE thread_id = ? AND state_volume = ?
			AND pact_session_id = (
				SELECT pact_session_id FROM runs
				WHERE id = ? AND status = 'running'
			)`,
		thread.SessionID,
		thread.ThreadID,
		thread.StateVolume,
		runID,
	)
	if err != nil {
		return fmt.Errorf("link Codex thread: validate thread: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("link Codex thread: read thread rows: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("link Codex thread: thread belongs to another session or state volume")
	}

	result, err = tx.ExecContext(ctx, `
		UPDATE runs
		SET thread_id = ?, user_agent = ?
		WHERE id = ? AND status = 'running'`,
		thread.ThreadID,
		thread.UserAgent,
		runID,
	)
	if err != nil {
		return fmt.Errorf("link Codex thread: update run: %w", err)
	}
	rows, err = result.RowsAffected()
	if err != nil {
		return fmt.Errorf("link Codex thread: read run rows: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("link Codex thread: running row %d not found", runID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("link Codex thread: commit: %w", err)
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
		UPDATE runs
		SET transcript_json = ?,
			transcript_captured_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ? AND thread_id IS NOT NULL`,
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
		return fmt.Errorf("store Codex transcript: thread for run %d not found", runID)
	}
	return nil
}
