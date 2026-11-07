-- +goose Up
ALTER TABLE node_executions ADD COLUMN subscription TEXT;
