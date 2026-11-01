-- Preserve old messages while retaining the signed question and incomplete
-- resource-set marker required to interpret an owner continuation safely.
ALTER TABLE messages ADD COLUMN clarification JSONB NOT NULL DEFAULT 'null';
ALTER TABLE messages ADD COLUMN files_unavailable BOOLEAN NOT NULL DEFAULT FALSE;
