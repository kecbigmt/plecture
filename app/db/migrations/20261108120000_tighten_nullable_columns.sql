-- +goose NO TRANSACTION
-- +goose Up
-- Rebuilding sessions with foreign keys enforced would cascade-delete every
-- row that references it, and the pragma that turns enforcement off is a
-- no-op inside goose's transaction, so this runs outside it in its own.
-- The resource-release view and triggers are dropped first because a table
-- rename re-parses every view and trigger, and theirs name the dropped
-- tables.
PRAGMA foreign_keys = off;
BEGIN IMMEDIATE;
DROP VIEW resource_references;
DROP TRIGGER population_members_release_resource;
DROP TRIGGER population_members_release_replaced_resource;
DROP TRIGGER subscription_retries_release_resource;
DROP TRIGGER subscription_retries_release_replaced_resource;
CREATE TABLE new_sessions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('down', 'up', 'destroyed')),
    sleep_since TEXT,
    destroyed_at TEXT,
    parent_session_id TEXT REFERENCES sessions(id) ON DELETE NO ACTION,
    root_session_id TEXT REFERENCES sessions(id) ON DELETE NO ACTION,
    resource_id TEXT REFERENCES resources(id),
    tag TEXT,
    alias TEXT,
    workflow TEXT NOT NULL,
    workflow_revision TEXT,
    project_root TEXT NOT NULL DEFAULT '',
    population_workflow TEXT,
    population_name TEXT,
    inputs_json TEXT NOT NULL CHECK (json_valid(inputs_json) AND json_type(inputs_json) = 'object'),
    health_last_checked_at TEXT,
    health_last_activity_at TEXT,
    health_last_fingerprint TEXT,
    health_last_state TEXT CHECK (health_last_state IS NULL OR health_last_state IN ('healthy', 'unhealthy', 'stalled', 'undeclared')),
    health_last_reason TEXT,
    health_last_notified_at TEXT,
    health_notify_count INTEGER,
    health_unevaluable_cycles INTEGER,
    tick_consecutive_unchanged INTEGER,
    tick_last_fingerprint TEXT,
    last_tick_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK ((status = 'destroyed') = (destroyed_at IS NOT NULL)),
    CHECK (NOT (parent_session_id IS NOT NULL AND root_session_id IS NOT NULL)),
    CHECK (root_session_id IS NULL OR root_session_id <> id),
    CHECK ((population_workflow IS NULL) = (population_name IS NULL)),
    CHECK ((health_notify_count IS NULL) = (health_unevaluable_cycles IS NULL)),
    CHECK (health_notify_count IS NOT NULL OR COALESCE(health_last_checked_at, health_last_activity_at, health_last_fingerprint, health_last_state, health_last_reason, health_last_notified_at) IS NULL),
    CHECK (tick_consecutive_unchanged IS NOT NULL OR tick_last_fingerprint IS NULL),
    FOREIGN KEY (project_root, population_workflow, population_name) REFERENCES populations(project_root, workflow, name) ON DELETE NO ACTION
);
INSERT INTO new_sessions (id, name, status, sleep_since, destroyed_at, parent_session_id, root_session_id, resource_id, tag, alias, workflow, workflow_revision, project_root, population_workflow, population_name, inputs_json, health_last_checked_at, health_last_activity_at, health_last_fingerprint, health_last_state, health_last_reason, health_last_notified_at, health_notify_count, health_unevaluable_cycles, tick_consecutive_unchanged, tick_last_fingerprint, last_tick_at, created_at, updated_at)
SELECT id, name, status, sleep_since, destroyed_at, parent_session_id, root_session_id, resource_id, tag, alias, workflow, workflow_revision, project_root, population_workflow, population_name, COALESCE(inputs_json, '{}'), health_last_checked_at, health_last_activity_at, health_last_fingerprint, health_last_state, health_last_reason, health_last_notified_at, COALESCE(health_notify_count, CASE WHEN COALESCE(health_last_checked_at, health_last_activity_at, health_last_fingerprint, health_last_state, health_last_reason, health_last_notified_at, health_unevaluable_cycles) IS NOT NULL THEN 0 END), COALESCE(health_unevaluable_cycles, CASE WHEN COALESCE(health_last_checked_at, health_last_activity_at, health_last_fingerprint, health_last_state, health_last_reason, health_last_notified_at, health_notify_count) IS NOT NULL THEN 0 END), COALESCE(tick_consecutive_unchanged, CASE WHEN tick_last_fingerprint IS NOT NULL THEN 0 END), tick_last_fingerprint, last_tick_at, created_at, updated_at
FROM sessions;
CREATE TABLE new_node_executions (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    sequence INTEGER NOT NULL,
    task_id TEXT,
    scope TEXT NOT NULL CHECK (scope IN ('session', 'run')),
    status TEXT NOT NULL CHECK (status IN ('produced', 'failed', 'cleaned')),
    resource_id TEXT REFERENCES resources(id),
    subscription_id TEXT REFERENCES resources(id),
    inputs_json TEXT NOT NULL CHECK (json_valid(inputs_json) AND json_type(inputs_json) = 'object'),
    outputs_json TEXT NOT NULL CHECK (json_valid(outputs_json) AND json_type(outputs_json) = 'object'),
    state_json TEXT NOT NULL CHECK (json_valid(state_json) AND json_type(state_json) = 'object'),
    resource_observation_json TEXT CHECK (resource_observation_json IS NULL OR (json_valid(resource_observation_json) AND json_type(resource_observation_json) = 'object')),
    resource_observed_at TEXT,
    done_when_json TEXT CHECK (done_when_json IS NULL OR (json_valid(done_when_json) AND json_type(done_when_json) = 'object')),
    extra_done_when_json TEXT CHECK (extra_done_when_json IS NULL OR json_valid(extra_done_when_json)),
    error TEXT,
    setup_at TEXT,
    failed_at TEXT,
    cleaned_at TEXT,
    finalized_at TEXT,
    process_json TEXT CHECK (process_json IS NULL OR (json_valid(process_json) AND json_type(process_json) = 'object')),
    directory TEXT,
    FOREIGN KEY (session_id, node_id) REFERENCES node_instances(session_id, node_id) ON DELETE CASCADE
);
INSERT INTO new_node_executions (id, session_id, node_id, sequence, task_id, scope, status, resource_id, subscription_id, inputs_json, outputs_json, state_json, resource_observation_json, resource_observed_at, done_when_json, extra_done_when_json, error, setup_at, failed_at, cleaned_at, finalized_at, process_json, directory)
SELECT id, session_id, node_id, sequence, task_id, scope, status, resource_id, subscription_id, COALESCE(inputs_json, '{}'), COALESCE(outputs_json, '{}'), COALESCE(state_json, '{}'), resource_observation_json, resource_observed_at, NULLIF(done_when_json, 'null'), extra_done_when_json, error, setup_at, failed_at, cleaned_at, finalized_at, process_json, directory
FROM node_executions;
CREATE TABLE new_node_execution_layers (
    execution_id TEXT NOT NULL REFERENCES node_executions(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    effect_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('produced', 'failed', 'cleaned')),
    inputs_json TEXT NOT NULL CHECK (json_valid(inputs_json) AND json_type(inputs_json) = 'object'),
    locals_json TEXT NOT NULL CHECK (json_valid(locals_json) AND json_type(locals_json) = 'object'),
    outputs_json TEXT NOT NULL CHECK (json_valid(outputs_json) AND json_type(outputs_json) = 'object'),
    env_json TEXT NOT NULL CHECK (json_valid(env_json) AND json_type(env_json) = 'object'),
    heartbeat_ticks INTEGER NOT NULL,
    heartbeat_escalations INTEGER NOT NULL,
    setup_at TEXT NOT NULL,
    failed_at TEXT,
    cleaned_at TEXT,
    error TEXT,
    PRIMARY KEY (execution_id, position)
);
INSERT INTO new_node_execution_layers (execution_id, position, effect_id, status, inputs_json, locals_json, outputs_json, env_json, heartbeat_ticks, heartbeat_escalations, setup_at, failed_at, cleaned_at, error)
SELECT execution_id, position, effect_id, status, COALESCE(inputs_json, '{}'), COALESCE(locals_json, '{}'), COALESCE(outputs_json, '{}'), COALESCE(env_json, '{}'), COALESCE(heartbeat_ticks, 0), COALESCE(heartbeat_escalations, 0), COALESCE(setup_at, (SELECT o.setup_at FROM node_executions o WHERE o.id = execution_id)), failed_at, cleaned_at, error
FROM node_execution_layers;
CREATE TABLE new_task_instances (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    instance_name TEXT NOT NULL,
    task_id TEXT NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('session', 'run')),
    status TEXT NOT NULL CHECK (status IN ('produced', 'failed', 'cleaned')),
    sequence INTEGER NOT NULL,
    resource_id TEXT REFERENCES resources(id),
    named boolean NOT NULL CHECK (named IN (0, 1)),
    inputs_json TEXT NOT NULL CHECK (json_valid(inputs_json) AND json_type(inputs_json) = 'object'),
    outputs_json TEXT NOT NULL CHECK (json_valid(outputs_json) AND json_type(outputs_json) = 'object'),
    state_json TEXT NOT NULL CHECK (json_valid(state_json) AND json_type(state_json) = 'object'),
    resource_observation_json TEXT CHECK (resource_observation_json IS NULL OR (json_valid(resource_observation_json) AND json_type(resource_observation_json) = 'object')),
    resource_observed_at TEXT,
    extra_done_when_json TEXT CHECK (extra_done_when_json IS NULL OR json_valid(extra_done_when_json)),
    error TEXT,
    setup_at TEXT,
    failed_at TEXT,
    cleaned_at TEXT,
    finalized_at TEXT,
    process_json TEXT CHECK (process_json IS NULL OR (json_valid(process_json) AND json_type(process_json) = 'object')),
    directory TEXT
);
INSERT INTO new_task_instances (id, session_id, instance_name, task_id, scope, status, sequence, resource_id, named, inputs_json, outputs_json, state_json, resource_observation_json, resource_observed_at, extra_done_when_json, error, setup_at, failed_at, cleaned_at, finalized_at, process_json, directory)
SELECT id, session_id, instance_name, task_id, scope, status, sequence, resource_id, named, COALESCE(inputs_json, '{}'), COALESCE(outputs_json, '{}'), COALESCE(state_json, '{}'), resource_observation_json, resource_observed_at, extra_done_when_json, error, setup_at, failed_at, cleaned_at, finalized_at, process_json, directory
FROM task_instances;
CREATE TABLE new_task_instance_layers (
    task_instance_id TEXT NOT NULL REFERENCES task_instances(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    effect_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('produced', 'failed', 'cleaned')),
    inputs_json TEXT NOT NULL CHECK (json_valid(inputs_json) AND json_type(inputs_json) = 'object'),
    locals_json TEXT NOT NULL CHECK (json_valid(locals_json) AND json_type(locals_json) = 'object'),
    outputs_json TEXT NOT NULL CHECK (json_valid(outputs_json) AND json_type(outputs_json) = 'object'),
    env_json TEXT NOT NULL CHECK (json_valid(env_json) AND json_type(env_json) = 'object'),
    heartbeat_ticks INTEGER NOT NULL,
    heartbeat_escalations INTEGER NOT NULL,
    setup_at TEXT NOT NULL,
    failed_at TEXT,
    cleaned_at TEXT,
    error TEXT,
    PRIMARY KEY (task_instance_id, position)
);
INSERT INTO new_task_instance_layers (task_instance_id, position, effect_id, status, inputs_json, locals_json, outputs_json, env_json, heartbeat_ticks, heartbeat_escalations, setup_at, failed_at, cleaned_at, error)
SELECT task_instance_id, position, effect_id, status, COALESCE(inputs_json, '{}'), COALESCE(locals_json, '{}'), COALESCE(outputs_json, '{}'), COALESCE(env_json, '{}'), COALESCE(heartbeat_ticks, 0), COALESCE(heartbeat_escalations, 0), COALESCE(setup_at, (SELECT o.setup_at FROM task_instances o WHERE o.id = task_instance_id)), failed_at, cleaned_at, error
FROM task_instance_layers;
DROP TABLE node_execution_layers;
DROP TABLE task_instance_layers;
DROP TABLE node_executions;
DROP TABLE task_instances;
DROP TABLE sessions;
ALTER TABLE new_sessions RENAME TO sessions;
ALTER TABLE new_node_executions RENAME TO node_executions;
ALTER TABLE new_node_execution_layers RENAME TO node_execution_layers;
ALTER TABLE new_task_instances RENAME TO task_instances;
ALTER TABLE new_task_instance_layers RENAME TO task_instance_layers;
CREATE UNIQUE INDEX sessions_live_name ON sessions(name) WHERE status <> 'destroyed';
CREATE INDEX sessions_alias_idx ON sessions(alias);
CREATE INDEX sessions_parent_idx ON sessions(parent_session_id);
CREATE INDEX sessions_resource_idx ON sessions(resource_id);
CREATE INDEX node_executions_session_node_idx ON node_executions(session_id, node_id, sequence);
CREATE UNIQUE INDEX node_executions_one_unreleased_idx ON node_executions(session_id, node_id) WHERE status <> 'cleaned';
CREATE INDEX node_executions_resource_idx ON node_executions(resource_id);
CREATE INDEX node_executions_subscription_idx ON node_executions(subscription_id);
CREATE UNIQUE INDEX task_instances_session_id_instance_name ON task_instances(session_id, instance_name);
CREATE INDEX task_instances_resource_idx ON task_instances(resource_id);
-- +goose StatementBegin
CREATE VIEW resource_references AS
SELECT resource_id FROM sessions WHERE resource_id IS NOT NULL
UNION ALL SELECT resource_id FROM task_instances WHERE resource_id IS NOT NULL
UNION ALL SELECT resource_id FROM node_executions WHERE resource_id IS NOT NULL
UNION ALL SELECT subscription_id FROM node_executions WHERE subscription_id IS NOT NULL
UNION ALL SELECT resource_id FROM population_members
UNION ALL SELECT resource_id FROM subscription_retries;

CREATE TRIGGER sessions_release_resource AFTER DELETE ON sessions BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER sessions_release_replaced_resource AFTER UPDATE OF resource_id ON sessions BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER task_instances_release_resource AFTER DELETE ON task_instances BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER task_instances_release_replaced_resource AFTER UPDATE OF resource_id ON task_instances BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER node_executions_release_resource AFTER DELETE ON node_executions BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
    DELETE FROM resources WHERE id = OLD.subscription_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.subscription_id);
END;

CREATE TRIGGER node_executions_release_replaced_resource AFTER UPDATE OF resource_id, subscription_id ON node_executions BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
    DELETE FROM resources WHERE id = OLD.subscription_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.subscription_id);
END;

CREATE TRIGGER population_members_release_resource AFTER DELETE ON population_members BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER population_members_release_replaced_resource AFTER UPDATE OF resource_id ON population_members BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER subscription_retries_release_resource AFTER DELETE ON subscription_retries BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;

CREATE TRIGGER subscription_retries_release_replaced_resource AFTER UPDATE OF resource_id ON subscription_retries BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;
-- +goose StatementEnd
COMMIT;
PRAGMA foreign_keys = on;
