-- +goose Up
ALTER TABLE node_executions ADD COLUMN subscription TEXT;

-- +goose Down
ALTER TABLE node_executions DROP COLUMN subscription;
