-- +goose Up
-- add column "workflow_revision" to table: "sessions"
ALTER TABLE `sessions` ADD COLUMN `workflow_revision` text NULL;
-- create "workflow_revisions" table
CREATE TABLE `workflow_revisions` (`digest` text NULL, `workflow` text NOT NULL, `body` text NOT NULL, `created_at` text NOT NULL, PRIMARY KEY (`digest`), CHECK (json_valid(body)));
CREATE TABLE `workflow_revision_migration` (`id` integer PRIMARY KEY CHECK (id = 1), `completed` integer NOT NULL CHECK (completed IN (0, 1)));
INSERT INTO `workflow_revision_migration` (`id`, `completed`)
SELECT 1, CASE WHEN EXISTS (SELECT 1 FROM `sessions` WHERE `status` <> 'destroyed' AND `workflow` <> '') THEN 0 ELSE 1 END;
