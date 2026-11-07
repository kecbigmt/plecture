-- +goose Up
-- The project root whose definitions configured the session, '' for the
-- global-only context. An invocation directory does not say which marked
-- root it would select, so every existing row starts in the global-only
-- context and an operator records the intended root by hand.
ALTER TABLE sessions DROP COLUMN invocation_dir;
ALTER TABLE sessions ADD COLUMN project_root TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Restoring the column cannot recover the directories it held.
ALTER TABLE sessions DROP COLUMN project_root;
ALTER TABLE sessions ADD COLUMN invocation_dir TEXT;
