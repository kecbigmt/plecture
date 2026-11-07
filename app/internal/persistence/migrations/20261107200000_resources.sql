-- +goose Up
-- Every table that referred to a resource by its identifier string now refers
-- to one resources row. The migration cannot evaluate a declaration's `name`,
-- so each distinct identifier becomes one row with no type, named by its own
-- id; a later resolution of that identifier records a typed row of its own.
-- PRAGMA foreign_keys is a no-op inside goose's transaction, so columns that
-- are not part of a key are added and dropped in place; the two tables keyed
-- by resource_id are rebuilt.
CREATE TABLE `resources` (`id` text NULL PRIMARY KEY, `type` text NOT NULL, `name` text NOT NULL, `locator` text NOT NULL, `created_at` text NOT NULL);
CREATE UNIQUE INDEX `resources_name` ON `resources` (`name`);
INSERT INTO `resources` (`id`, `type`, `name`, `locator`, `created_at`)
SELECT lower(hex(randomblob(16))), '', `locator`, `locator`, strftime('%Y-%m-%dT%H:%M:%f', 'now') || '000000Z'
FROM (
    SELECT `resource_id` AS `locator` FROM `sessions` WHERE `resource_id` IS NOT NULL
    UNION SELECT `resource` FROM `task_instances` WHERE `resource` IS NOT NULL
    UNION SELECT `resource` FROM `node_executions` WHERE `resource` IS NOT NULL
    UNION SELECT `subscription` FROM `node_executions` WHERE `subscription` IS NOT NULL
    UNION SELECT `resource_id` FROM `population_members`
    UNION SELECT `resource_id` FROM `subscription_retries`
);
UPDATE `resources` SET `name` = `id`;

ALTER TABLE `sessions` ADD COLUMN `resource_ref` text NULL REFERENCES `resources` (`id`);
UPDATE `sessions` SET `resource_ref` = (SELECT `id` FROM `resources` WHERE `locator` = `sessions`.`resource_id`) WHERE `resource_id` IS NOT NULL;
ALTER TABLE `sessions` DROP COLUMN `resource_id`;
ALTER TABLE `sessions` DROP COLUMN `resource_type`;
ALTER TABLE `sessions` RENAME COLUMN `resource_ref` TO `resource_id`;
CREATE INDEX `sessions_resource_idx` ON `sessions` (`resource_id`);

ALTER TABLE `task_instances` ADD COLUMN `resource_id` text NULL REFERENCES `resources` (`id`);
UPDATE `task_instances` SET `resource_id` = (SELECT `id` FROM `resources` WHERE `locator` = `task_instances`.`resource`) WHERE `resource` IS NOT NULL;
ALTER TABLE `task_instances` DROP COLUMN `resource`;
CREATE INDEX `task_instances_resource_idx` ON `task_instances` (`resource_id`);

ALTER TABLE `node_executions` ADD COLUMN `resource_id` text NULL REFERENCES `resources` (`id`);
UPDATE `node_executions` SET `resource_id` = (SELECT `id` FROM `resources` WHERE `locator` = `node_executions`.`resource`) WHERE `resource` IS NOT NULL;
ALTER TABLE `node_executions` DROP COLUMN `resource`;
CREATE INDEX `node_executions_resource_idx` ON `node_executions` (`resource_id`);
ALTER TABLE `node_executions` ADD COLUMN `subscription_id` text NULL REFERENCES `resources` (`id`);
UPDATE `node_executions` SET `subscription_id` = (SELECT `id` FROM `resources` WHERE `locator` = `node_executions`.`subscription`) WHERE `subscription` IS NOT NULL;
ALTER TABLE `node_executions` DROP COLUMN `subscription`;
CREATE INDEX `node_executions_subscription_idx` ON `node_executions` (`subscription_id`);

CREATE TABLE `new_subscription_retries` (`session_id` text NOT NULL, `action` text NOT NULL, `resource_id` text NOT NULL, PRIMARY KEY (`session_id`, `action`, `resource_id`), CONSTRAINT `0` FOREIGN KEY (`resource_id`) REFERENCES `resources` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION, CONSTRAINT `1` FOREIGN KEY (`session_id`) REFERENCES `sessions` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CHECK (action IN ('subscribe', 'unsubscribe')));
INSERT INTO `new_subscription_retries` (`session_id`, `action`, `resource_id`)
SELECT `r`.`session_id`, `r`.`action`, `res`.`id`
FROM `subscription_retries` AS `r` JOIN `resources` AS `res` ON `res`.`locator` = `r`.`resource_id`;
DROP TABLE `subscription_retries`;
ALTER TABLE `new_subscription_retries` RENAME TO `subscription_retries`;
CREATE INDEX `subscription_retries_resource_idx` ON `subscription_retries` (`resource_id`);

CREATE TABLE `new_population_members` (`workflow` text NOT NULL, `name` text NOT NULL, `resource_id` text NOT NULL, `session_name` text NULL, `generation` integer NOT NULL DEFAULT 0, `accepted_at` text NULL, `last_appearance` text NULL, `last_inbound` text NULL, `tombstoned` boolean NOT NULL DEFAULT 0, `pending_up` boolean NOT NULL DEFAULT 0, `consecutive_admit_failures` integer NOT NULL DEFAULT 0, `last_admit_reason` text NULL, `last_admit_error` text NULL, `admit_retry_at` text NULL, `admit_suspended` boolean NOT NULL DEFAULT 0, `decision_kind` text NULL, `decision_reason` text NULL, `item_json` text NOT NULL DEFAULT '{}', PRIMARY KEY (`workflow`, `name`, `resource_id`), CONSTRAINT `0` FOREIGN KEY (`workflow`, `name`) REFERENCES `populations` (`workflow`, `name`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `1` FOREIGN KEY (`resource_id`) REFERENCES `resources` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION, CHECK (tombstoned IN (0, 1)), CHECK (pending_up IN (0, 1)), CHECK (admit_suspended IN (0, 1)), CHECK (decision_kind IN ('plect.workflow_population.destroy', 'plect.workflow_population.destroy_deferred', 'plect.workflow_population.destroy_dry_run')), CHECK (json_valid(item_json)));
INSERT INTO `new_population_members` (`workflow`, `name`, `resource_id`, `session_name`, `generation`, `accepted_at`, `last_appearance`, `last_inbound`, `tombstoned`, `pending_up`, `consecutive_admit_failures`, `last_admit_reason`, `last_admit_error`, `admit_retry_at`, `admit_suspended`, `decision_kind`, `decision_reason`, `item_json`)
SELECT `m`.`workflow`, `m`.`name`, `res`.`id`, `m`.`session_name`, `m`.`generation`, `m`.`accepted_at`, `m`.`last_appearance`, `m`.`last_inbound`, `m`.`tombstoned`, `m`.`pending_up`, `m`.`consecutive_admit_failures`, `m`.`last_admit_reason`, `m`.`last_admit_error`, `m`.`admit_retry_at`, `m`.`admit_suspended`, `m`.`decision_kind`, `m`.`decision_reason`, `m`.`item_json`
FROM `population_members` AS `m` JOIN `resources` AS `res` ON `res`.`locator` = `m`.`resource_id`;
CREATE TABLE `new_population_member_blockers` (`workflow` text NOT NULL, `name` text NOT NULL, `resource_id` text NOT NULL, `position` integer NOT NULL, `reason` text NOT NULL, PRIMARY KEY (`workflow`, `name`, `resource_id`, `position`), CONSTRAINT `0` FOREIGN KEY (`workflow`, `name`, `resource_id`) REFERENCES `new_population_members` (`workflow`, `name`, `resource_id`) ON UPDATE NO ACTION ON DELETE CASCADE);
INSERT INTO `new_population_member_blockers` (`workflow`, `name`, `resource_id`, `position`, `reason`)
SELECT `b`.`workflow`, `b`.`name`, `res`.`id`, `b`.`position`, `b`.`reason`
FROM `population_member_blockers` AS `b` JOIN `resources` AS `res` ON `res`.`locator` = `b`.`resource_id`;
DROP TABLE `population_member_blockers`;
DROP TABLE `population_members`;
ALTER TABLE `new_population_members` RENAME TO `population_members`;
ALTER TABLE `new_population_member_blockers` RENAME TO `population_member_blockers`;
CREATE INDEX `population_members_resource_idx` ON `population_members` (`resource_id`);
