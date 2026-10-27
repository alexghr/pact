ALTER TABLE sessions ADD COLUMN workspace_id INTEGER REFERENCES workspaces(id);
ALTER TABLE sessions ADD COLUMN storage_key TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN state_volume TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN nix_volume TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN provisioned INTEGER NOT NULL DEFAULT 0;

UPDATE sessions SET (workspace_id, storage_key, state_volume, nix_volume, provisioned) =
    (SELECT workspace_id, id, state_volume, nix_volume, provisioned
     FROM environments WHERE environments.id = sessions.environment_id);
ALTER TABLE sessions DROP COLUMN environment_id;
DROP TABLE environments;
