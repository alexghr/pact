-- Thread activity uses Unix nanoseconds; native rollout events can share a
-- millisecond. Execution timestamps retain their existing millisecond format.
CREATE TABLE threads (
    session_id     INTEGER NOT NULL REFERENCES sessions(id),
    id             TEXT NOT NULL,
    parent_id      TEXT NOT NULL DEFAULT '',
    title          TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    last_activity  INTEGER NOT NULL,
    activity_known INTEGER NOT NULL DEFAULT 0,
    archived       INTEGER NOT NULL DEFAULT 0,
    internal       INTEGER NOT NULL DEFAULT 0,
    missing        INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (session_id, id)
);

ALTER TABLE executions ADD COLUMN thread_id TEXT NOT NULL DEFAULT '';
