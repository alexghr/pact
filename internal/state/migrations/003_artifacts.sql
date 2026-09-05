CREATE TABLE artifacts (
	id INTEGER PRIMARY KEY,
	creator_pact_session_id INTEGER NOT NULL,
	updated_by_pact_session_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	FOREIGN KEY (creator_pact_session_id) REFERENCES pact_sessions(id),
	FOREIGN KEY (updated_by_pact_session_id) REFERENCES pact_sessions(id)
);

CREATE INDEX artifacts_creator_idx
	ON artifacts (creator_pact_session_id, id);

CREATE TABLE artifact_files (
	artifact_id INTEGER NOT NULL,
	path TEXT NOT NULL,
	media_type TEXT NOT NULL,
	content BLOB NOT NULL,
	size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
	sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
	version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
	updated_by_pact_session_id INTEGER NOT NULL,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (artifact_id, path),
	FOREIGN KEY (artifact_id) REFERENCES artifacts(id),
	FOREIGN KEY (updated_by_pact_session_id) REFERENCES pact_sessions(id)
);
