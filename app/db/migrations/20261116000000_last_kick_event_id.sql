-- +goose Up
ALTER TABLE task_done_when_states ADD COLUMN last_kick_event_id TEXT;
ALTER TABLE events ADD COLUMN supersedes TEXT NOT NULL DEFAULT '';
