CREATE TABLE IF NOT EXISTS schema_version (
    migration  TEXT PRIMARY KEY,
    sha256     BLOB NOT NULL,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);
