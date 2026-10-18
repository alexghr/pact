-- Environment IDs also identify Docker ownership labels and Nix roots, so they
-- remain text. Workspace and execution IDs become integer catalogue keys.
CREATE TABLE workspaces_new (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    path       TEXT NOT NULL UNIQUE,
    owner      TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE environments_new (
    id           TEXT PRIMARY KEY,
    workspace_id INTEGER NOT NULL REFERENCES workspaces_new(id),
    state_volume TEXT NOT NULL,
    nix_volume   TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    provisioned  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE sessions_new (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    environment_id TEXT NOT NULL REFERENCES environments_new(id),
    owner          TEXT NOT NULL,
    backend        TEXT NOT NULL CHECK (backend <> ''),
    created_at     INTEGER NOT NULL
);

CREATE TABLE executions_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  INTEGER NOT NULL REFERENCES sessions_new(id),
    settings    TEXT NOT NULL CHECK (json_valid(settings)),
    status      TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    started_at  INTEGER,
    finished_at INTEGER,
    exit_code   INTEGER,
    error       TEXT NOT NULL DEFAULT ''
);

-- The old TEXT-primary-key tables already have integer rowids. Use those to
-- translate references without changing session numbers or external resources.
INSERT INTO workspaces_new
SELECT rowid, path, owner, created_at FROM workspaces;

INSERT INTO environments_new
SELECT e.id, w.rowid, e.state_volume, e.nix_volume, e.created_at, e.provisioned
FROM environments e JOIN workspaces w ON w.id = e.workspace_id;

INSERT INTO sessions_new
SELECT id, environment_id, owner, backend, created_at FROM sessions;

INSERT INTO executions_new
SELECT rowid, session_id, settings, status, created_at, started_at, finished_at, exit_code, error
FROM executions;

-- Retain the highest issued session number, including deleted sessions and an
-- empty catalogue, so the migration cannot cause an old number to be reused.
INSERT INTO sqlite_sequence(name, seq)
SELECT 'sessions_new', seq FROM sqlite_sequence
WHERE name = 'sessions'
AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'sessions_new');

UPDATE sqlite_sequence
SET seq = MAX(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'sessions'), 0))
WHERE name = 'sessions_new';

DROP TABLE executions;
DROP TABLE sessions;
DROP TABLE environments;
DROP TABLE workspaces;

ALTER TABLE workspaces_new RENAME TO workspaces;
ALTER TABLE environments_new RENAME TO environments;
ALTER TABLE sessions_new RENAME TO sessions;
ALTER TABLE executions_new RENAME TO executions;
