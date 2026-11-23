-- +goose Up
CREATE TABLE unanswered_deliveries (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    event_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    delivered_at TEXT NOT NULL
);
