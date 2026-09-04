CREATE TABLE repositories (
	id INTEGER PRIMARY KEY,
	url TEXT NOT NULL,
	clone_url TEXT NOT NULL,
	push_url TEXT NOT NULL,
	name TEXT NOT NULL,
	default_branch TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE pact_session_repositories (
	id INTEGER PRIMARY KEY,
	pact_session_id INTEGER NOT NULL,
	repository_id INTEGER NOT NULL,
	checkout_dir TEXT NOT NULL,
	UNIQUE (pact_session_id, checkout_dir),
	FOREIGN KEY (pact_session_id) REFERENCES pact_sessions(id),
	FOREIGN KEY (repository_id) REFERENCES repositories(id)
);
