-- +goose Up
ALTER TABLE events ADD COLUMN supersedes TEXT NOT NULL DEFAULT '';
