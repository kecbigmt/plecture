-- +goose Up
-- The entry resource a session was created for: its resource type, the
-- instance name that resource derived, and the caller's tag. NULL for every
-- existing row, because none of them recorded which resource type produced
-- its name; a resource-addressed up that composes such a name reports a
-- collision instead of guessing.
ALTER TABLE sessions ADD COLUMN resource_type TEXT;
ALTER TABLE sessions ADD COLUMN instance_name TEXT;
ALTER TABLE sessions ADD COLUMN tag TEXT;

-- +goose Down
-- Dropping the columns discards the recorded entry resources.
ALTER TABLE sessions DROP COLUMN tag;
ALTER TABLE sessions DROP COLUMN instance_name;
ALTER TABLE sessions DROP COLUMN resource_type;
