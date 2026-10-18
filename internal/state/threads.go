package state

import (
	"context"
	"fmt"
)

// Thread is catalogue metadata, not a copy of the conversation. Timestamps are
// Unix nanoseconds. LastUsed is stored in the legacy last_activity column;
// activity_known is retained only for migration compatibility.
type Thread struct {
	ID        string `json:"id"`
	SessionID int64  `json:"sessionId"`
	ParentID  string `json:"parentId,omitempty"`
	Title     string `json:"title,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	LastUsed  int64  `json:"lastUsed"`
	Archived  bool   `json:"archived"`
	Internal  bool   `json:"internal"`
	Missing   bool   `json:"missing"`
}

func (s *Store) Threads(ctx context.Context, sessionID int64) ([]Thread, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, parent_id, title, created_at, last_activity,
		       archived, internal, missing
		FROM threads WHERE session_id = ? ORDER BY last_activity DESC, id DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}
	defer rows.Close()
	threads := []Thread{}
	for rows.Next() {
		var thread Thread
		if err := rows.Scan(&thread.ID, &thread.SessionID, &thread.ParentID, &thread.Title,
			&thread.CreatedAt, &thread.LastUsed,
			&thread.Archived, &thread.Internal, &thread.Missing); err != nil {
			return nil, fmt.Errorf("read thread: %w", err)
		}
		threads = append(threads, thread)
	}
	return threads, rows.Err()
}

const recordThread = `
		INSERT INTO threads(session_id, id, parent_id, title, created_at, last_activity,
		                    archived, internal, missing)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, id) DO UPDATE SET
		    parent_id = excluded.parent_id, title = excluded.title,
		    created_at = excluded.created_at, last_activity = excluded.last_activity,
		    archived = excluded.archived,
		    internal = excluded.internal, missing = excluded.missing`

func (s *Store) RecordThread(ctx context.Context, thread Thread) error {
	if thread.ID == "" {
		return fmt.Errorf("native thread ID is empty")
	}
	_, err := s.db.ExecContext(ctx, recordThread,
		thread.SessionID, thread.ID, thread.ParentID, thread.Title, thread.CreatedAt,
		thread.LastUsed, thread.Archived, thread.Internal, thread.Missing)
	if err != nil {
		return fmt.Errorf("record thread: %w", err)
	}
	return nil
}

// ObserveThread durably links the running execution to its native thread before
// any later output or failure and records when that thread was last used.
func (s *Store) ObserveThread(ctx context.Context, executionID int64, threadID string, at int64) error {
	if threadID == "" {
		return fmt.Errorf("native thread ID is empty")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin thread observation: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE executions SET thread_id = ? WHERE id = ? AND (thread_id = '' OR thread_id = ?)`,
		threadID, executionID, threadID)
	if err := executionUpdate(result, err); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO threads(session_id, id, created_at, last_activity)
		SELECT session_id, ?, ?, ? FROM executions WHERE id = ?
		ON CONFLICT(session_id, id) DO UPDATE SET
		    last_activity = MAX(last_activity, excluded.last_activity),
		    missing = 0`, threadID, at, at, executionID); err != nil {
		return fmt.Errorf("observe thread: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit thread observation: %w", err)
	}
	return nil
}
