package state

import (
	"context"
	"fmt"
)

// RefreshThreads replaces cached metadata as one snapshot. Missing entries
// remain recorded so a lost conversation never becomes a new, empty session.
func (s *Store) RefreshThreads(ctx context.Context, sessionID int64, threads []Thread) error {
	seen := make(map[string]bool, len(threads))
	for _, thread := range threads {
		if thread.ID == "" || thread.CreatedAt <= 0 || thread.LastUsed < thread.CreatedAt {
			return fmt.Errorf("invalid native thread metadata")
		}
		if seen[thread.ID] {
			return fmt.Errorf("ambiguous history: duplicate thread %s", thread.ID)
		}
		seen[thread.ID] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE threads SET missing = 1 WHERE session_id = ?", sessionID); err != nil {
		return err
	}
	for _, thread := range threads {
		if _, err := tx.ExecContext(ctx, recordThread,
			sessionID, thread.ID, thread.ParentID, thread.Title, thread.CreatedAt,
			thread.LastUsed, thread.Archived, thread.Internal, thread.Missing); err != nil {
			return fmt.Errorf("refresh thread %s: %w", thread.ID, err)
		}
	}
	return tx.Commit()
}
