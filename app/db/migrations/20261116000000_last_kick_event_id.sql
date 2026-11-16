-- +goose Up
ALTER TABLE task_done_when_states ADD COLUMN last_kick_event_id TEXT;
