package state

import (
	"strings"
	"testing"
	"testing/fstest"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func TestCataloguePersists(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	files := fstest.MapFS{
		"migrations/0002_records.sql": {Data: []byte("CREATE TABLE records (value TEXT PRIMARY KEY)")},
		"migrations/0003_seed.sql":    {Data: []byte("INSERT INTO records VALUES ('retained')")},
	}
	if err := store.migrate(t.Context(), files); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = openStore(t)
	files["migrations/0004_append.sql"] = &fstest.MapFile{
		Data: []byte("INSERT INTO records VALUES ('pending')"),
	}
	// Reapplying the seed would violate its primary key; only the new SQL runs.
	if err := store.migrate(t.Context(), files); err != nil {
		t.Fatalf("repeat migrations with pending SQL: %v", err)
	}
	var values string
	if err := store.db.QueryRowContext(t.Context(),
		"SELECT group_concat(value, ',') FROM (SELECT value FROM records ORDER BY value)",
	).Scan(&values); err != nil {
		t.Fatal(err)
	}
	if values != "pending,retained" {
		t.Fatalf("stored values = %q, want pending,retained", values)
	}
}

func TestMigrationErrorRollsBack(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	files := fstest.MapFS{
		"migrations/0002_bad.sql": {Data: []byte("CREATE TABLE partial (id INTEGER); INVALID SQL")},
	}
	err := store.migrate(t.Context(), files)
	if err == nil || !strings.Contains(err.Error(), "0002_bad.sql") {
		t.Fatalf("migration error = %v, want failed migration name", err)
	}
	var count int
	if err := store.db.QueryRowContext(t.Context(),
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'partial'",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed migration left a partial table")
	}
	if err := store.db.QueryRowContext(t.Context(),
		"SELECT count(*) FROM schema_version WHERE migration = 'migrations/0002_bad.sql'",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed migration was recorded as applied")
	}
	files["migrations/0002_bad.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE partial (id INTEGER)")}
	if err := store.migrate(t.Context(), files); err != nil {
		t.Fatalf("retry corrected migration: %v", err)
	}
}

func TestMigrationChangedAfterApplying(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	files := fstest.MapFS{
		"migrations/0002_records.sql": {Data: []byte("CREATE TABLE original (id INTEGER)")},
	}
	if err := store.migrate(t.Context(), files); err != nil {
		t.Fatal(err)
	}
	files["migrations/0002_records.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE changed (id INTEGER)")}
	err := store.migrate(t.Context(), files)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") || !strings.Contains(err.Error(), "0002_records.sql") {
		t.Fatalf("changed migration error = %v, want checksum mismatch and migration name", err)
	}
	var count int
	if err := store.db.QueryRowContext(t.Context(),
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'changed'",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("changed migration was executed")
	}
}
