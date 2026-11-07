-- +goose Up
-- The directory whose ancestor overlays configured the session at creation.
-- NULL means none were layered, which is what every session created before
-- this column saw from a chain or population and is the safe reading for the
-- rest: a rebase of such a session expands plugin and global layers alone.
ALTER TABLE sessions ADD COLUMN invocation_dir TEXT;

-- +goose Down
-- Dropping the column discards the recorded contexts.
ALTER TABLE sessions DROP COLUMN invocation_dir;
