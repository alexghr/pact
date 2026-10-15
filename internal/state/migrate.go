package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
)

//go:embed migrations/*.sql
var migrations embed.FS

func (s *Store) migrate(ctx context.Context, files fs.FS) error {
	// History is bootstrap metadata, separate from versioned migrations.
	schema, err := migrations.ReadFile("migrations/schema_version.sql")
	if err != nil {
		return fmt.Errorf("read migration history schema: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, string(schema)); err != nil {
		return fmt.Errorf("create migration history: %w", err)
	}

	// Glob returns sorted filenames; numbered migrations use zero-padded names.
	names, err := fs.Glob(files, "migrations/[0-9]*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	for _, name := range names {
		query, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := s.applyMigration(ctx, name, string(query)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, name, query string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer tx.Rollback()

	hash := sha256.Sum256([]byte(query))
	result, err := tx.ExecContext(ctx,
		"INSERT INTO schema_version(migration, sha256) VALUES(?, ?) ON CONFLICT(migration) DO NOTHING",
		name, hash[:],
	)
	if err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check migration insert: %w", err)
	}
	if inserted == 0 {
		var storedHash []byte
		if err := tx.QueryRowContext(ctx,
			"SELECT sha256 FROM schema_version WHERE migration = ?", name,
		).Scan(&storedHash); err != nil {
			return fmt.Errorf("read migration checksum: %w", err)
		}
		if !bytes.Equal(storedHash, hash[:]) {
			return fmt.Errorf("checksum mismatch")
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("apply SQL: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}
