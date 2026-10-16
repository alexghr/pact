ALTER TABLE environments ADD COLUMN provisioned INTEGER NOT NULL DEFAULT 0;

CREATE TABLE shared_storage (
    singleton   INTEGER PRIMARY KEY CHECK (singleton = 1),
    volume      TEXT NOT NULL UNIQUE,
    provisioned INTEGER NOT NULL DEFAULT 0
);
