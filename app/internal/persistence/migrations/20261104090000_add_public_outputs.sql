-- +goose Up
-- add column "public_outputs_json" to table: "sessions"
ALTER TABLE `sessions` ADD COLUMN `public_outputs_json` text NULL CHECK (public_outputs_json IS NULL OR json_valid(public_outputs_json));

-- +goose Down
-- reverse: add column "public_outputs_json" to table: "sessions"
ALTER TABLE `sessions` DROP COLUMN `public_outputs_json`;
