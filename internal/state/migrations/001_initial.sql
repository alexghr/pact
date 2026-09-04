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
);
