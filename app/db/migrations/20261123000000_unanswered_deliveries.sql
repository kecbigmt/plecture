-- +goose Up
CREATE TABLE unanswered_deliveries (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    event_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    delivered_at TEXT NOT NULL,
    PRIMARY KEY (session_id, event_id)
);
