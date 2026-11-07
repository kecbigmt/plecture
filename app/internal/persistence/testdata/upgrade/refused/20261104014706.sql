PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE goose_db_version (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		version_id INTEGER NOT NULL,
		is_applied INTEGER NOT NULL,
		tstamp TIMESTAMP DEFAULT (datetime('now'))
	);
INSERT INTO goose_db_version VALUES(1,0,1,'2026-11-07 14:32:01');
INSERT INTO goose_db_version VALUES(2,20260906051929,1,'2026-11-07 14:32:01');
INSERT INTO goose_db_version VALUES(3,20260906071015,1,'2026-11-07 14:32:01');
INSERT INTO goose_db_version VALUES(4,20260906085730,1,'2026-11-07 14:32:01');
INSERT INTO goose_db_version VALUES(5,20260906143514,1,'2026-11-07 14:32:01');
INSERT INTO goose_db_version VALUES(6,20260906231449,1,'2026-11-07 14:32:01');
INSERT INTO goose_db_version VALUES(7,20260907002408,1,'2026-11-07 14:32:01');
INSERT INTO goose_db_version VALUES(8,20260907085032,1,'2026-11-07 14:32:01');
INSERT INTO goose_db_version VALUES(9,20260907221219,1,'2026-11-07 14:32:01');
INSERT INTO goose_db_version VALUES(10,20260908130000,1,'2026-11-07 14:32:01');
INSERT INTO goose_db_version VALUES(11,20261008155752,1,'2026-11-07 14:32:01');
INSERT INTO goose_db_version VALUES(12,20261103194111,1,'2026-11-07 14:32:02');
INSERT INTO goose_db_version VALUES(13,20261103200131,1,'2026-11-07 14:32:02');
INSERT INTO goose_db_version VALUES(14,20261104000000,1,'2026-11-07 14:32:02');
INSERT INTO goose_db_version VALUES(15,20261104010220,1,'2026-11-07 14:32:02');
INSERT INTO goose_db_version VALUES(16,20261104014706,1,'2026-11-07 14:32:02');
CREATE TABLE `task_done_when_judges` (`task_instance_id` text NOT NULL, `leaf_id` text NOT NULL, `action` text NOT NULL, `reason` text NOT NULL, `revision` text NOT NULL, `judge_session` text NOT NULL, `judge_workflow` text NULL, `relation` text NOT NULL, `created_at` text NOT NULL, PRIMARY KEY (`task_instance_id`, `leaf_id`), CONSTRAINT `0` FOREIGN KEY (`task_instance_id`) REFERENCES `task_instances` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CHECK (action IN ('approve', 'request_changes')), CHECK (relation IN ('self', 'parent', 'child', 'sibling', 'ancestor', 'descendant', 'unrelated')));
INSERT INTO task_done_when_judges VALUES('01TASK','review','approve','looks right','rev-1','triage-7-review',NULL,'child','2026-09-07T10:00:04.000000000Z');
CREATE TABLE `populations` (`workflow` text NOT NULL, `name` text NOT NULL, PRIMARY KEY (`workflow`, `name`));
INSERT INTO populations VALUES('triage','inbox');
CREATE TABLE `up_reservations` (`child_session_name` text NULL, `parent_session_name` text NULL, `virtual_root` boolean NOT NULL DEFAULT 0, `pid` integer NOT NULL, `reserved_at` text NOT NULL, PRIMARY KEY (`child_session_name`), CHECK (virtual_root IN (0, 1)), CHECK ((parent_session_name IS NOT NULL) != (virtual_root = 1)));
CREATE TABLE `sessions` (
  `id` text NULL,
  `name` text NOT NULL,
  `status` text NOT NULL,
  `destroyed_at` text NULL,
  `parent_session_id` text NULL,
  `root_session_id` text NULL,
  `resource_id` text NULL,
  `alias` text NULL,
  `workflow` text NOT NULL,
  `workspace_dir` text NULL,
  `population_workflow` text NULL,
  `population_name` text NULL,
  `inputs_json` text NULL,
  `health_last_checked_at` text NULL,
  `health_last_activity_at` text NULL,
  `health_last_fingerprint` text NULL,
  `health_last_state` text NULL,
  `health_last_reason` text NULL,
  `health_last_notified_at` text NULL,
  `health_notify_count` integer NULL,
  `tick_consecutive_unchanged` integer NULL,
  `tick_last_fingerprint` text NULL,
  `last_tick_at` text NULL,
  `created_at` text NOT NULL,
  `updated_at` text NOT NULL, `health_unevaluable_cycles` integer NULL, `sleep_since` text NULL, `workflow_revision` text NULL,
  PRIMARY KEY (`id`),
  CONSTRAINT `0` FOREIGN KEY (`population_workflow`, `population_name`) REFERENCES `populations` (`workflow`, `name`) ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT `1` FOREIGN KEY (`root_session_id`) REFERENCES `sessions` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT `2` FOREIGN KEY (`parent_session_id`) REFERENCES `sessions` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION,
  CHECK (status IN ('down', 'up', 'destroyed')),
  CHECK (inputs_json IS NULL OR json_valid(inputs_json)),
  CHECK (health_last_state IS NULL OR health_last_state IN ('healthy', 'unhealthy', 'stalled', 'undeclared')),
  CHECK ((status = 'destroyed') = (destroyed_at IS NOT NULL)),
  CHECK (NOT (parent_session_id IS NOT NULL AND root_session_id IS NOT NULL)),
  CHECK (root_session_id IS NULL OR root_session_id <> id),
  CHECK ((population_workflow IS NULL) = (population_name IS NULL))
);
INSERT INTO sessions VALUES('01SROOT','triage-7','up',NULL,NULL,NULL,'tracker:item/7',NULL,'triage',NULL,'triage','inbox','{"item":"7"}',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,'2026-09-07T10:00:01.000000000Z','2026-09-07T10:00:01.000000000Z',NULL,NULL,'sha256:0001');
INSERT INTO sessions VALUES('01SCHILD','triage-7-review','down',NULL,'01SROOT',NULL,NULL,NULL,'review',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,'2026-09-07T10:00:02.000000000Z','2026-09-07T10:00:03.000000000Z',NULL,NULL,NULL);
INSERT INTO sessions VALUES('01SGONE','old-run','destroyed','2026-09-07T09:00:00.000000000Z',NULL,NULL,NULL,NULL,'triage',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,'2026-09-07T08:00:00.000000000Z','2026-09-07T09:00:00.000000000Z',NULL,NULL,NULL);
CREATE TABLE IF NOT EXISTS "task_instances" (`id` text NULL, `session_id` text NOT NULL, `instance_name` text NOT NULL, `task_id` text NOT NULL, `scope` text NOT NULL, `status` text NOT NULL, `sequence` integer NOT NULL, `resource` text NULL, `named` boolean NOT NULL, `inputs_json` text NULL, `outputs_json` text NULL, `state_json` text NULL, `resource_observation_json` text NULL, `resource_observed_at` text NULL, `extra_done_when_json` text NULL, `error` text NULL, `setup_at` text NULL, `failed_at` text NULL, `cleaned_at` text NULL, `finalized_at` text NULL, `process_json` text NULL CHECK (process_json IS NULL OR json_valid(process_json)), PRIMARY KEY (`id`), CONSTRAINT `0` FOREIGN KEY (`session_id`) REFERENCES `sessions` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CHECK (scope IN ('session', 'run')), CHECK (status IN ('produced', 'failed', 'cleaned')), CHECK (named IN (0, 1)), CHECK (inputs_json IS NULL OR json_valid(inputs_json)), CHECK (outputs_json IS NULL OR json_valid(outputs_json)), CHECK (state_json IS NULL OR json_valid(state_json)), CHECK (resource_observation_json IS NULL OR json_valid(resource_observation_json)), CHECK (extra_done_when_json IS NULL OR json_valid(extra_done_when_json)));
INSERT INTO task_instances VALUES('01TASK','01SROOT','initial','pursue','session','produced',1,'tracker:item/7',1,'{"item":"7"}',NULL,NULL,NULL,NULL,NULL,NULL,'2026-09-07T10:00:01.000000000Z',NULL,NULL,NULL,NULL);
CREATE TABLE IF NOT EXISTS "task_done_when_states" (`task_instance_id` text NULL, `heartbeat_ticks` integer NOT NULL DEFAULT 0, `heartbeat_escalations` integer NOT NULL DEFAULT 0, `last_action` text NULL, `last_fingerprint` text NULL, `last_reason` text NULL, `last_body` text NULL, `escalated_at` text NULL, `escalate_reason` text NULL, PRIMARY KEY (`task_instance_id`), CONSTRAINT `0` FOREIGN KEY (`task_instance_id`) REFERENCES `task_instances` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CHECK (last_action IN ('satisfied', 'wait', 'escalate', 'review_required', 'kick')));
INSERT INTO task_done_when_states VALUES('01TASK',2,0,'wait',NULL,'checks pending',NULL,NULL,NULL);
CREATE TABLE `task_done_when_unsatisfied_items` (`task_instance_id` text NOT NULL, `position` integer NOT NULL, `item` text NOT NULL, PRIMARY KEY (`task_instance_id`, `position`), CONSTRAINT `0` FOREIGN KEY (`task_instance_id`) REFERENCES `task_instances` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
INSERT INTO task_done_when_unsatisfied_items VALUES('01TASK',0,'checks pass');
CREATE TABLE `population_member_blockers` (`workflow` text NOT NULL, `name` text NOT NULL, `resource_id` text NOT NULL, `position` integer NOT NULL, `reason` text NOT NULL, PRIMARY KEY (`workflow`, `name`, `resource_id`, `position`), CONSTRAINT `0` FOREIGN KEY (`workflow`, `name`, `resource_id`) REFERENCES `population_members` (`workflow`, `name`, `resource_id`) ON UPDATE NO ACTION ON DELETE CASCADE);
INSERT INTO population_member_blockers VALUES('triage','inbox','tracker:item/7',0,'waiting for review');
CREATE TABLE `task_instance_layers` (`task_instance_id` text NOT NULL, `position` integer NOT NULL, `effect_id` text NOT NULL, `status` text NOT NULL, `inputs_json` text NULL, `locals_json` text NULL, `outputs_json` text NULL, `env_json` text NULL, `heartbeat_ticks` integer NULL, `heartbeat_escalations` integer NULL, `setup_at` text NULL, `failed_at` text NULL, `cleaned_at` text NULL, `error` text NULL, PRIMARY KEY (`task_instance_id`, `position`), CONSTRAINT `0` FOREIGN KEY (`task_instance_id`) REFERENCES `task_instances` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CHECK (status IN ('produced', 'failed', 'cleaned')), CHECK (inputs_json IS NULL OR json_valid(inputs_json)), CHECK (locals_json IS NULL OR json_valid(locals_json)), CHECK (outputs_json IS NULL OR json_valid(outputs_json)), CHECK (env_json IS NULL OR json_valid(env_json)));
INSERT INTO task_instance_layers VALUES('01TASK',0,'checkout','produced',NULL,NULL,'{"dir":"/work/7"}',NULL,NULL,NULL,'2026-09-07T10:00:01.000000000Z',NULL,NULL,NULL);
CREATE TABLE `session_channel_health` (`session_id` text NOT NULL, `kind` text NOT NULL, `consecutive_failures` integer NOT NULL, `first_failure_at` text NOT NULL, `last_failure_at` text NOT NULL, `last_channel` text NULL, `last_error` text NULL, `escalated_at` text NULL, PRIMARY KEY (`session_id`, `kind`), CONSTRAINT `0` FOREIGN KEY (`session_id`) REFERENCES `sessions` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CHECK (kind IN ('validation', 'delivery')));
INSERT INTO session_channel_health VALUES('01SROOT','delivery',1,'2026-09-07T10:00:05.000000000Z','2026-09-07T10:00:05.000000000Z','chat','timeout',NULL);
CREATE TABLE IF NOT EXISTS "events" (`id` text NULL, `session_id` text NOT NULL, `sequence` integer NOT NULL, `time` text NOT NULL, `type` text NOT NULL, `source` text NOT NULL, `direction` text NOT NULL, `summary` text NOT NULL, `body` text NOT NULL DEFAULT '', `metadata_json` text NOT NULL, PRIMARY KEY (`id`), CONSTRAINT `0` FOREIGN KEY (`session_id`) REFERENCES `sessions` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION, CHECK (sequence > 0), CHECK (direction IN ('inbound', 'outbound', 'internal')), CHECK (json_valid(metadata_json)));
INSERT INTO events VALUES('01EV1','01SROOT',1,'2026-09-07T10:00:01.000000000Z','plect.instruction','plect','inbound','initial instruction','Resolve item 7.','{"task":"initial"}');
INSERT INTO events VALUES('01EV2','01SCHILD',1,'2026-09-07T10:00:03.000000000Z','lifecycle.down','plect','internal','session down','','{}');
CREATE TABLE `node_instances` (`session_id` text NOT NULL, `node_id` text NOT NULL, PRIMARY KEY (`session_id`, `node_id`), CONSTRAINT `0` FOREIGN KEY (`session_id`) REFERENCES `sessions` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
INSERT INTO node_instances VALUES('01SROOT','agent');
INSERT INTO node_instances VALUES('01SROOT','@workflow');
CREATE TABLE `node_executions` (`id` text NULL, `session_id` text NOT NULL, `node_id` text NOT NULL, `sequence` integer NOT NULL, `task_id` text NULL, `name` text NULL, `scope` text NOT NULL, `status` text NOT NULL, `resource` text NULL, `inputs_json` text NULL, `outputs_json` text NULL, `state_json` text NULL, `resource_observation_json` text NULL, `resource_observed_at` text NULL, `done_when_json` text NULL, `extra_done_when_json` text NULL, `error` text NULL, `setup_at` text NULL, `failed_at` text NULL, `cleaned_at` text NULL, `finalized_at` text NULL, `process_json` text NULL CHECK (process_json IS NULL OR json_valid(process_json)), PRIMARY KEY (`id`), CONSTRAINT `0` FOREIGN KEY (`session_id`, `node_id`) REFERENCES `node_instances` (`session_id`, `node_id`) ON UPDATE NO ACTION ON DELETE CASCADE, CHECK (scope IN ('session', 'run')), CHECK (status IN ('produced', 'failed', 'cleaned')), CHECK (inputs_json IS NULL OR json_valid(inputs_json)), CHECK (outputs_json IS NULL OR json_valid(outputs_json)), CHECK (state_json IS NULL OR json_valid(state_json)), CHECK (resource_observation_json IS NULL OR json_valid(resource_observation_json)), CHECK (done_when_json IS NULL OR json_valid(done_when_json)), CHECK (extra_done_when_json IS NULL OR json_valid(extra_done_when_json)));
INSERT INTO node_executions VALUES('01EXAGENT','01SROOT','agent',1,'pursue',NULL,'session','produced','tracker:item/7',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,'2026-09-07T10:00:01.000000000Z',NULL,NULL,NULL,NULL);
INSERT INTO node_executions VALUES('01EXWORKFLOW','01SROOT','@workflow',1,NULL,NULL,'session','produced',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,'2026-09-07T10:00:01.000000000Z',NULL,NULL,NULL,NULL);
CREATE TABLE `node_execution_layers` (`execution_id` text NOT NULL, `position` integer NOT NULL, `effect_id` text NOT NULL, `status` text NOT NULL, `inputs_json` text NULL, `locals_json` text NULL, `outputs_json` text NULL, `env_json` text NULL, `heartbeat_ticks` integer NULL, `heartbeat_escalations` integer NULL, `setup_at` text NULL, `failed_at` text NULL, `cleaned_at` text NULL, `error` text NULL, PRIMARY KEY (`execution_id`, `position`), CONSTRAINT `0` FOREIGN KEY (`execution_id`) REFERENCES `node_executions` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CHECK (status IN ('produced', 'failed', 'cleaned')), CHECK (inputs_json IS NULL OR json_valid(inputs_json)), CHECK (locals_json IS NULL OR json_valid(locals_json)), CHECK (outputs_json IS NULL OR json_valid(outputs_json)), CHECK (env_json IS NULL OR json_valid(env_json)));
INSERT INTO node_execution_layers VALUES('01EXAGENT',0,'terminal','produced',NULL,NULL,'{"pane":"1"}',NULL,NULL,NULL,'2026-09-07T10:00:01.000000000Z',NULL,NULL,NULL);
CREATE TABLE `node_execution_dependencies` (`execution_id` text NOT NULL, `depends_on_execution_id` text NOT NULL, PRIMARY KEY (`execution_id`, `depends_on_execution_id`), CONSTRAINT `0` FOREIGN KEY (`depends_on_execution_id`) REFERENCES `node_executions` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `1` FOREIGN KEY (`execution_id`) REFERENCES `node_executions` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CHECK (execution_id <> depends_on_execution_id));
CREATE TABLE IF NOT EXISTS "event_cursors" (`session_id` text NOT NULL, `kind` text NOT NULL, `next_sequence` integer NOT NULL, PRIMARY KEY (`session_id`, `kind`), CONSTRAINT `0` FOREIGN KEY (`session_id`) REFERENCES `sessions` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CHECK (kind IN ('delivery', 'tick', 'heartbeat', 'forward')), CHECK (next_sequence >= 0));
INSERT INTO event_cursors VALUES('01SROOT','delivery',2);
CREATE TABLE chain_attempts (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    instance TEXT NOT NULL,
    chain_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    PRIMARY KEY (session_id, instance, chain_id)
);
INSERT INTO chain_attempts VALUES('01SROOT','initial','review','fp-1');
CREATE TABLE subscription_retries (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('subscribe', 'unsubscribe')),
    resource_id TEXT NOT NULL,
    PRIMARY KEY (session_id, action, resource_id)
);
INSERT INTO subscription_retries VALUES('01SROOT','subscribe','tracker:item/7');
CREATE TABLE IF NOT EXISTS "population_members" (`workflow` text NOT NULL, `name` text NOT NULL, `resource_id` text NOT NULL, `session_name` text NULL, `generation` integer NOT NULL DEFAULT 0, `accepted_at` text NULL, `last_appearance` text NULL, `last_inbound` text NULL, `tombstoned` boolean NOT NULL DEFAULT 0, `pending_up` boolean NOT NULL DEFAULT 0, `consecutive_admit_failures` integer NOT NULL DEFAULT 0, `last_admit_reason` text NULL, `last_admit_error` text NULL, `admit_retry_at` text NULL, `admit_suspended` boolean NOT NULL DEFAULT 0, `decision_kind` text NULL, `decision_reason` text NULL, `item_json` text NOT NULL DEFAULT '{}', PRIMARY KEY (`workflow`, `name`, `resource_id`), CONSTRAINT `0` FOREIGN KEY (`workflow`, `name`) REFERENCES `populations` (`workflow`, `name`) ON UPDATE NO ACTION ON DELETE CASCADE, CHECK (tombstoned IN (0, 1)), CHECK (pending_up IN (0, 1)), CHECK (admit_suspended IN (0, 1)), CHECK (decision_kind IN ('plect.workflow_population.destroy', 'plect.workflow_population.destroy_deferred', 'plect.workflow_population.destroy_dry_run')), CHECK (json_valid(item_json)));
INSERT INTO population_members VALUES('triage','inbox','tracker:item/7','triage-7',1,'2026-09-07T10:00:00.000000000Z','2026-09-07T10:00:00.000000000Z',NULL,0,0,0,NULL,NULL,NULL,0,NULL,NULL,'{"title":"Seven"}');
CREATE TABLE `workflow_revisions` (`digest` text NULL, `workflow` text NOT NULL, `body` text NOT NULL, `created_at` text NOT NULL, PRIMARY KEY (`digest`), CHECK (json_valid(body)));
INSERT INTO workflow_revisions VALUES('sha256:0001','triage','{"nodes":{}}','2026-11-04T10:00:00.000000000Z');
CREATE TABLE `workflow_revision_migration` (`id` integer PRIMARY KEY CHECK (id = 1), `completed` integer NOT NULL CHECK (completed IN (0, 1)));
INSERT INTO workflow_revision_migration VALUES(1,1);
PRAGMA writable_schema=ON;
CREATE TABLE IF NOT EXISTS sqlite_sequence(name,seq);
DELETE FROM sqlite_sequence;
INSERT INTO sqlite_sequence VALUES('goose_db_version',16);
CREATE UNIQUE INDEX `sessions_live_name` ON `sessions` (`name`) WHERE status <> 'destroyed';
CREATE INDEX `sessions_alias_idx` ON `sessions` (`alias`);
CREATE INDEX `sessions_parent_idx` ON `sessions` (`parent_session_id`);
CREATE UNIQUE INDEX `task_instances_session_id_instance_name` ON `task_instances` (`session_id`, `instance_name`);
CREATE UNIQUE INDEX `events_session_id_sequence` ON `events` (`session_id`, `sequence`);
CREATE INDEX `events_session_id_id_idx` ON `events` (`session_id`, `id`);
CREATE INDEX `events_session_id_type_sequence_idx` ON `events` (`session_id`, `type`, `sequence`);
CREATE INDEX `node_executions_session_node_idx` ON `node_executions` (`session_id`, `node_id`, `sequence`);
CREATE UNIQUE INDEX `node_executions_one_unreleased_idx` ON `node_executions` (`session_id`, `node_id`) WHERE status <> 'cleaned';
CREATE INDEX `node_execution_dependencies_depends_on_idx` ON `node_execution_dependencies` (`depends_on_execution_id`);
CREATE INDEX `events_repair_state_change_idx` ON `events` (`session_id`, `type`, `sequence`) WHERE (
    json_extract(metadata_json, '$.result') IN ('started', 'suspended')
    OR (json_extract(metadata_json, '$.result') = 'succeeded' AND json_extract(metadata_json, '$.attempt') = '0')
  );
PRAGMA writable_schema=OFF;
COMMIT;
