-- +goose Up
-- add column "process_json" to table: "node_executions"
ALTER TABLE `node_executions` ADD COLUMN `process_json` text NULL CHECK (process_json IS NULL OR json_valid(process_json));
-- add column "process_json" to table: "task_instances"
ALTER TABLE `task_instances` ADD COLUMN `process_json` text NULL CHECK (process_json IS NULL OR json_valid(process_json));
