package state

import (
	"database/sql"
	"io/fs"
	"path/filepath"
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

func TestUpgradeRetainsSessionBindingAndStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db, path: path}
	old := fstest.MapFS{}
	names, err := fs.Glob(migrations, "migrations/000*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		data, err := fs.ReadFile(migrations, name)
		if err != nil {
			t.Fatal(err)
		}
		old[name] = &fstest.MapFile{Data: data}
	}
	if err := store.migrate(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	_, err = db.Exec(`
        INSERT INTO workspaces(id,path,owner,created_at) VALUES(1,?,'operator',1);
        INSERT INTO environments(id,workspace_id,state_volume,nix_volume,created_at,provisioned)
          VALUES('LegacyRoot',1,'pact-state-legacyroot','pact-nix-existing',1,1);
        INSERT INTO sessions(id,environment_id,owner,backend,created_at,title)
          VALUES(9,'LegacyRoot','operator','codex',1,'Retained work');
        INSERT INTO executions(id,session_id,settings,status,created_at,thread_id)
          VALUES(1,9,'{"model":"saved","dind":true,"hostNet":true}','succeeded',1,'original');
        INSERT INTO executions(id,session_id,settings,status,created_at,thread_id)
          VALUES(2,9,'{"model":"saved","dind":true,"hostNet":true}','succeeded',2,'later-native-thread');
        `, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(t.Context(), migrations); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Session(t.Context(), 9)
	if err != nil {
		t.Fatal(err)
	}
	if saved.BackendRef != "original" || !saved.Settings.Capabilities.DIND || !saved.Settings.Capabilities.HostNetwork || saved.Workspace != workspace || saved.Storage.Key != "LegacyRoot" ||
		saved.Storage.StateVolume != "pact-state-legacyroot" || saved.Storage.NixVolume != "pact-nix-existing" || !saved.Storage.Provisioned {
		t.Fatalf("upgrade changed session identity or storage: %+v", saved)
	}
	if err := store.BindBackendRef(t.Context(), 9, "another"); err != nil {
		t.Fatal(err)
	}
	resumed, _, err := store.ResumeLaunch(t.Context(), 9)
	if err != nil || resumed.Storage != saved.Storage || resumed.Workspace != workspace {
		t.Fatalf("upgrade lost resume resources: %+v, %v", resumed, err)
	}
}
