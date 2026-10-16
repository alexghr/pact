-- Timestamps are Unix milliseconds.
CREATE TABLE workspaces (
    id         TEXT PRIMARY KEY,
    path       TEXT NOT NULL UNIQUE,
    owner      TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE environments (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    state_volume TEXT NOT NULL,
    nix_volume   TEXT NOT NULL,
    created_at   INTEGER NOT NULL
);

CREATE TABLE sessions (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    environment_id TEXT NOT NULL REFERENCES environments(id),
    owner          TEXT NOT NULL,
    backend        TEXT NOT NULL CHECK (backend <> ''),
    created_at     INTEGER NOT NULL
);

CREATE TABLE executions (
    id          TEXT PRIMARY KEY,
    session_id  INTEGER NOT NULL REFERENCES sessions(id),
    settings    TEXT NOT NULL CHECK (json_valid(settings)),
    status      TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    started_at  INTEGER,
    finished_at INTEGER,
    exit_code   INTEGER,
    error       TEXT NOT NULL DEFAULT ''
);
