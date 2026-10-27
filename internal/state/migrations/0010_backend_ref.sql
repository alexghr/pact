ALTER TABLE sessions ADD COLUMN backend_ref TEXT NOT NULL DEFAULT '';

-- Keep the first observed native conversation when upgrading an existing session.
UPDATE sessions SET backend_ref = COALESCE(
    (SELECT thread_id FROM executions WHERE session_id = sessions.id AND thread_id <> '' ORDER BY id LIMIT 1),
    (SELECT id FROM threads WHERE session_id = sessions.id AND internal = 0 ORDER BY created_at, id LIMIT 1),
    '');
