-- +goose Up
-- add column "workflow_revision" to table: "sessions"
ALTER TABLE `sessions` ADD COLUMN `workflow_revision` text NULL;
-- create "workflow_revisions" table
CREATE TABLE `workflow_revisions` (`digest` text NULL, `workflow` text NOT NULL, `body` text NOT NULL, `created_at` text NOT NULL, PRIMARY KEY (`digest`), CHECK (json_valid(body)));

-- +goose Down
-- reverse: create "workflow_revisions" table
DROP TABLE `workflow_revisions`;
-- reverse: add column "workflow_revision" to table: "sessions"
ALTER TABLE `sessions` DROP COLUMN `workflow_revision`;
