CREATE TABLE execution_resources (
    name         TEXT PRIMARY KEY,
    execution_id INTEGER NOT NULL REFERENCES executions(id),
    kind         TEXT NOT NULL CHECK (kind IN ('container', 'volume')),
    image        TEXT NOT NULL DEFAULT '',
    container_id TEXT NOT NULL DEFAULT '',
    image_id     TEXT NOT NULL DEFAULT '',
    cleaned      INTEGER NOT NULL DEFAULT 0
);
