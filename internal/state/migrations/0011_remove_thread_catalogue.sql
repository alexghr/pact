DROP TABLE threads;
ALTER TABLE executions DROP COLUMN thread_id;
ALTER TABLE sessions DROP COLUMN reconciled_at;
ALTER TABLE sessions DROP COLUMN reconciliation_error;
