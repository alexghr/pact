package state

import (
	"context"
	"errors"
)

// BindBackendRef retains the initial native conversation. Later native navigation
// must not redirect the Pact session to a different conversation.
func (s *Store) BindBackendRef(ctx context.Context, sessionID int64, ref string) error {
	if ref == "" {
		return errors.New("backend reference is empty")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE sessions
        SET backend_ref = CASE WHEN backend_ref = '' THEN ? ELSE backend_ref END WHERE id = ?`, ref, sessionID)
	return executionUpdate(result, err)
}
