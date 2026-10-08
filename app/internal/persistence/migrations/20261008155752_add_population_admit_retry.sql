-- +goose Up
-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_population_members" table
CREATE TABLE `new_population_members` (`workflow` text NOT NULL, `name` text NOT NULL, `resource_id` text NOT NULL, `session_name` text NULL, `generation` integer NOT NULL DEFAULT 0, `accepted_at` text NULL, `last_appearance` text NULL, `last_inbound` text NULL, `tombstoned` boolean NOT NULL DEFAULT 0, `pending_up` boolean NOT NULL DEFAULT 0, `consecutive_admit_failures` integer NOT NULL DEFAULT 0, `last_admit_reason` text NULL, `last_admit_error` text NULL, `admit_retry_at` text NULL, `admit_suspended` boolean NOT NULL DEFAULT 0, `decision_kind` text NULL, `decision_reason` text NULL, `item_json` text NOT NULL DEFAULT '{}', PRIMARY KEY (`workflow`, `name`, `resource_id`), CONSTRAINT `0` FOREIGN KEY (`workflow`, `name`) REFERENCES `populations` (`workflow`, `name`) ON UPDATE NO ACTION ON DELETE CASCADE, CHECK (tombstoned IN (0, 1)), CHECK (pending_up IN (0, 1)), CHECK (admit_suspended IN (0, 1)), CHECK (decision_kind IN ('plect.workflow_population.destroy', 'plect.workflow_population.destroy_deferred', 'plect.workflow_population.destroy_dry_run')), CHECK (json_valid(item_json)));
-- copy rows from old table "population_members" to new temporary table "new_population_members"
INSERT INTO `new_population_members` (`workflow`, `name`, `resource_id`, `session_name`, `generation`, `accepted_at`, `last_appearance`, `last_inbound`, `tombstoned`, `pending_up`, `decision_kind`, `decision_reason`, `item_json`) SELECT `workflow`, `name`, `resource_id`, `session_name`, `generation`, `accepted_at`, `last_appearance`, `last_inbound`, `tombstoned`, `pending_up`, `decision_kind`, `decision_reason`, `item_json` FROM `population_members`;
-- drop "population_members" table after copying rows
DROP TABLE `population_members`;
-- rename temporary table "new_population_members" to "population_members"
ALTER TABLE `new_population_members` RENAME TO `population_members`;
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;

-- +goose Down
PRAGMA foreign_keys = off;
CREATE TABLE `old_population_members` (`workflow` text NOT NULL, `name` text NOT NULL, `resource_id` text NOT NULL, `session_name` text NULL, `generation` integer NOT NULL DEFAULT 0, `accepted_at` text NULL, `last_appearance` text NULL, `last_inbound` text NULL, `tombstoned` boolean NOT NULL DEFAULT 0, `pending_up` boolean NOT NULL DEFAULT 0, `decision_kind` text NULL, `decision_reason` text NULL, `item_json` text NOT NULL DEFAULT '{}', PRIMARY KEY (`workflow`, `name`, `resource_id`), CONSTRAINT `0` FOREIGN KEY (`workflow`, `name`) REFERENCES `populations` (`workflow`, `name`) ON UPDATE NO ACTION ON DELETE CASCADE, CHECK (tombstoned IN (0, 1)), CHECK (pending_up IN (0, 1)), CHECK (decision_kind IN ('plect.workflow_population.destroy', 'plect.workflow_population.destroy_deferred', 'plect.workflow_population.destroy_dry_run')), CHECK (json_valid(item_json)));
INSERT INTO `old_population_members` (`workflow`, `name`, `resource_id`, `session_name`, `generation`, `accepted_at`, `last_appearance`, `last_inbound`, `tombstoned`, `pending_up`, `decision_kind`, `decision_reason`, `item_json`) SELECT `workflow`, `name`, `resource_id`, `session_name`, `generation`, `accepted_at`, `last_appearance`, `last_inbound`, `tombstoned`, `pending_up`, `decision_kind`, `decision_reason`, `item_json` FROM `population_members`;
DROP TABLE `population_members`;
ALTER TABLE `old_population_members` RENAME TO `population_members`;
PRAGMA foreign_keys = on;
