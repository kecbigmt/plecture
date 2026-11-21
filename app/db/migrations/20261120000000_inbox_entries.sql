-- +goose Up
CREATE TABLE inbox_entries (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    channel TEXT NOT NULL,
    batch_id TEXT,
    deliveries INTEGER NOT NULL DEFAULT 0 CHECK (deliveries >= 0),
    PRIMARY KEY (session_id, sequence)
);
