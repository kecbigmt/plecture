-- schema.sql is the hand-edited declarative authority for the database
-- structure described in docs/design/sqlite-persistence.md. Every change
-- here needs a hand-written, forward-only goose migration under
-- migrations/ that reaches the same structure; the schema drift test
-- fails until one does.
--
-- Runtime, event, and retry-state tables live here. A destroyed sessions row
-- is the tombstone for that incarnation.
--
-- Nullability convention throughout: a column is NULL exactly when the
-- domain value can be genuinely absent (never observed/resolved yet, or an
-- optional fact); a column that domain logic always populates is NOT NULL
-- with no DEFAULT, so every write site states its value explicitly rather
-- than silently inheriting one from the database.
--
-- Timestamp encoding: every *_at / *_json timestamp column is a UTC
-- RFC3339 string with exactly nine fractional digits (e.g.
-- "2026-09-06T08:50:42.821423717Z"), or NULL when unset — never a
-- variable-width fractional part, so lexical order equals time order. See
-- timeconv.go.
CREATE TABLE workflow_revisions (
    digest TEXT PRIMARY KEY,
    workflow TEXT NOT NULL,
    body TEXT NOT NULL CHECK (json_valid(body)),
    created_at TEXT NOT NULL
);

CREATE TABLE workflow_revision_migration (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    completed INTEGER NOT NULL CHECK (completed IN (0, 1))
);

-- name is the slug a declaration's `name` derived, unique across types because
-- a session takes its name from it. locator is the identifier first resolved
-- to the row; a later spelling never replaces it. id is opaque: migrated rows
-- carry another id shape, and nothing reads it. An empty type marks an
-- identifier no declaration resolved, migrated rows included; such a row is
-- named by its own id so it never holds a derived name.
CREATE TABLE resources (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    locator TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX resources_name ON resources(name);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('down', 'up', 'destroyed')),
    sleep_since TEXT,
    destroyed_at TEXT,
    parent_session_id TEXT REFERENCES sessions(id) ON DELETE NO ACTION,
    root_session_id TEXT REFERENCES sessions(id) ON DELETE NO ACTION,
    -- The entry resource a session was created for; NULL for a session no
    -- resource created.
    resource_id TEXT REFERENCES resources(id),
    tag TEXT,
    alias TEXT,
    workflow TEXT NOT NULL,
    workflow_revision TEXT,
    project_root TEXT NOT NULL DEFAULT '',
    population_workflow TEXT,
    population_name TEXT,
    inputs_json TEXT NOT NULL CHECK (json_valid(inputs_json) AND json_type(inputs_json) = 'object'),
    -- health_* is a derived runtime observation, not a lifecycle status;
    -- see docs/design/sqlite-persistence.md.
    health_last_checked_at TEXT,
    health_last_activity_at TEXT,
    health_last_fingerprint TEXT,
    health_last_state TEXT CHECK (health_last_state IS NULL OR health_last_state IN ('healthy', 'unhealthy', 'stalled', 'undeclared')),
    health_last_reason TEXT,
    health_last_notified_at TEXT,
    health_notify_count INTEGER,
    health_unevaluable_cycles INTEGER,
    -- Tick backoff's own log-position watermark lives in event_cursors'
    -- `heartbeat` kind instead of a column here; see that table.
    tick_consecutive_unchanged INTEGER,
    tick_last_fingerprint TEXT,
    last_tick_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK ((status = 'destroyed') = (destroyed_at IS NOT NULL)),
    CHECK (NOT (parent_session_id IS NOT NULL AND root_session_id IS NOT NULL)),
    CHECK (root_session_id IS NULL OR root_session_id <> id),
    CHECK ((population_workflow IS NULL) = (population_name IS NULL)),
    -- health_notify_count and tick_consecutive_unchanged are NULL exactly
    -- when the session has no health state or tick backoff, so no other
    -- column of that group is set without its marker.
    CHECK ((health_notify_count IS NULL) = (health_unevaluable_cycles IS NULL)),
    CHECK (health_notify_count IS NOT NULL OR COALESCE(health_last_checked_at, health_last_activity_at, health_last_fingerprint, health_last_state, health_last_reason, health_last_notified_at) IS NULL),
    CHECK (tick_consecutive_unchanged IS NOT NULL OR tick_last_fingerprint IS NULL),
    -- A population-owned session shares its population's configuration
    -- context, so project_root is both the session's context and the first
    -- column of the population it names. No code path deletes a population
    -- row; SET NULL would null project_root, which is NOT NULL.
    FOREIGN KEY (project_root, population_workflow, population_name) REFERENCES populations(project_root, workflow, name) ON DELETE NO ACTION
);

-- Unique only among live rows; a destroyed session's name is free to reuse.
CREATE UNIQUE INDEX sessions_live_name ON sessions(name) WHERE status <> 'destroyed';
CREATE INDEX sessions_alias_idx ON sessions(alias);
CREATE INDEX sessions_parent_idx ON sessions(parent_session_id);
CREATE INDEX sessions_resource_idx ON sessions(resource_id);

-- A cap-refusal streak is scoped to a session incarnation, not its reusable
-- name. An empty fingerprint is represented by no row.
CREATE TABLE chain_attempts (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    instance TEXT NOT NULL,
    chain_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    PRIMARY KEY (session_id, instance, chain_id)
);

-- Failed provider subscribe/unsubscribe intents await an opportunistic retry.
CREATE TABLE subscription_retries (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('subscribe', 'unsubscribe')),
    resource_id TEXT NOT NULL REFERENCES resources(id),
    PRIMARY KEY (session_id, action, resource_id)
);

CREATE INDEX subscription_retries_resource_idx ON subscription_retries(resource_id);

-- Static workflow-DAG nodes' identity only; see node_executions below.
CREATE TABLE node_instances (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    PRIMARY KEY (session_id, node_id)
);

-- One row per setup attempt; see node_executions_one_unreleased_idx below.
CREATE TABLE node_executions (
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

CREATE INDEX node_executions_session_node_idx ON node_executions(session_id, node_id, sequence);
CREATE UNIQUE INDEX node_executions_one_unreleased_idx ON node_executions(session_id, node_id) WHERE status <> 'cleaned';
CREATE INDEX node_executions_resource_idx ON node_executions(resource_id);
CREATE INDEX node_executions_subscription_idx ON node_executions(subscription_id);

-- One row per layer of one execution's nested effect chain.
CREATE TABLE node_execution_layers (
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

-- execution_id is the dependent (released first), depends_on_execution_id the prerequisite.
CREATE TABLE node_execution_dependencies (
    execution_id TEXT NOT NULL REFERENCES node_executions(id) ON DELETE CASCADE,
    depends_on_execution_id TEXT NOT NULL REFERENCES node_executions(id) ON DELETE CASCADE,
    PRIMARY KEY (execution_id, depends_on_execution_id),
    CHECK (execution_id <> depends_on_execution_id)
);

CREATE INDEX node_execution_dependencies_depends_on_idx ON node_execution_dependencies(depends_on_execution_id);

-- id is preserved across an update; only a cleanup+setup mints a fresh one.
CREATE TABLE task_instances (
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

CREATE UNIQUE INDEX task_instances_session_id_instance_name ON task_instances(session_id, instance_name);
CREATE INDEX task_instances_resource_idx ON task_instances(resource_id);

-- See node_instance_layers; keyed by task_instances.id.
CREATE TABLE task_instance_layers (
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

CREATE TABLE task_done_when_states (
    task_instance_id TEXT PRIMARY KEY REFERENCES task_instances(id) ON DELETE CASCADE,
    heartbeat_ticks INTEGER NOT NULL DEFAULT 0,
    heartbeat_escalations INTEGER NOT NULL DEFAULT 0,
    last_action TEXT CHECK (last_action IN ('satisfied', 'wait', 'escalate', 'review_required', 'kick')),
    last_fingerprint TEXT,
    last_reason TEXT,
    last_body TEXT,
    escalated_at TEXT,
    escalate_reason TEXT
);

-- Ordered list; each heartbeat evaluation replaces it wholesale rather than diffing.
CREATE TABLE task_done_when_unsatisfied_items (
    task_instance_id TEXT NOT NULL REFERENCES task_instances(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    item TEXT NOT NULL,
    PRIMARY KEY (task_instance_id, position)
);

-- judge_session is a stored fact (the verdict must still read correctly
-- after the judge session is destroyed); judge_workflow is nullable
-- since a judge created via the legacy inline-tasks path has none. The
-- judged side is not stored here at all: task_instance_id's own parent row
-- (session_id, instance_name) is always the judged session/instance, so
-- the contract's DoneWhenJudge carries no separate target field for it.
CREATE TABLE task_done_when_judges (
    task_instance_id TEXT NOT NULL REFERENCES task_instances(id) ON DELETE CASCADE,
    leaf_id TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('approve', 'request_changes')),
    reason TEXT NOT NULL,
    revision TEXT NOT NULL,
    judge_session TEXT NOT NULL,
    judge_workflow TEXT,
    relation TEXT NOT NULL CHECK (relation IN ('self', 'parent', 'child', 'sibling', 'ancestor', 'descendant', 'unrelated')),
    created_at TEXT NOT NULL,
    PRIMARY KEY (task_instance_id, leaf_id)
);

-- (project_root, workflow, name) is the population's own domain identity:
-- the configuration context that declares it ('' for the global-only
-- context), its workflow's address, and its name.
CREATE TABLE populations (
    project_root TEXT NOT NULL DEFAULT '',
    workflow TEXT NOT NULL,
    name TEXT NOT NULL,
    PRIMARY KEY (project_root, workflow, name)
);

-- session_name is a recorded fact, not a foreign key: a poll/appearance can
-- accept a member and record its intended session name before that session's own row exists.
CREATE TABLE population_members (
    project_root TEXT NOT NULL DEFAULT '',
    workflow TEXT NOT NULL,
    name TEXT NOT NULL,
    resource_id TEXT NOT NULL REFERENCES resources(id),
    session_name TEXT,
    generation INTEGER NOT NULL DEFAULT 0,
    accepted_at TEXT,
    last_appearance TEXT,
    last_inbound TEXT,
    tombstoned boolean NOT NULL DEFAULT 0 CHECK (tombstoned IN (0, 1)),
    pending_up boolean NOT NULL DEFAULT 0 CHECK (pending_up IN (0, 1)),
    consecutive_admit_failures INTEGER NOT NULL DEFAULT 0,
    last_admit_reason TEXT,
    last_admit_error TEXT,
    admit_retry_at TEXT,
    admit_suspended boolean NOT NULL DEFAULT 0 CHECK (admit_suspended IN (0, 1)),
    decision_kind TEXT CHECK (decision_kind IN ('plect.workflow_population.destroy', 'plect.workflow_population.destroy_deferred', 'plect.workflow_population.destroy_dry_run')),
    decision_reason TEXT,
    item_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(item_json)),
    PRIMARY KEY (project_root, workflow, name, resource_id),
    FOREIGN KEY (project_root, workflow, name) REFERENCES populations(project_root, workflow, name) ON DELETE CASCADE
);

CREATE INDEX population_members_resource_idx ON population_members(resource_id);

-- Ordered list; same replace-wholesale pattern as task_done_when_unsatisfied_items.
CREATE TABLE population_member_blockers (
    project_root TEXT NOT NULL DEFAULT '',
    workflow TEXT NOT NULL,
    name TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    position INTEGER NOT NULL,
    reason TEXT NOT NULL,
    PRIMARY KEY (project_root, workflow, name, resource_id, position),
    FOREIGN KEY (project_root, workflow, name, resource_id) REFERENCES population_members(project_root, workflow, name, resource_id) ON DELETE CASCADE
);

-- Two independent failure streaks (validation, delivery); no row when open.
CREATE TABLE session_channel_health (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('validation', 'delivery')),
    consecutive_failures INTEGER NOT NULL,
    first_failure_at TEXT NOT NULL,
    last_failure_at TEXT NOT NULL,
    last_channel TEXT,
    last_error TEXT,
    escalated_at TEXT,
    PRIMARY KEY (session_id, kind)
);

-- No foreign key to sessions: a reservation exists for a child session that
-- does not exist yet (it is being created); parent_session_name likewise
-- names a parent that may not exist yet, and NULL rather than a sentinel
-- string means "counted against the virtual root's own cap" (a parentless
-- session, or one whose parent is the "root:" pseudo-parent) — the domain
-- layer's VirtualRootReservationParent sentinel lives only in Go, never in
-- this column.
CREATE TABLE up_reservations (
    child_session_name TEXT PRIMARY KEY,
    parent_session_name TEXT,
    virtual_root boolean NOT NULL DEFAULT 0 CHECK (virtual_root IN (0, 1)),
    pid INTEGER NOT NULL,
    reserved_at TEXT NOT NULL,
    CHECK ((parent_session_name IS NOT NULL) != (virtual_root = 1))
);

-- events/event_cursors key off sessions(id) directly: a row now is one incarnation.
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    time TEXT NOT NULL,
    type TEXT NOT NULL,
    source TEXT NOT NULL,
    direction TEXT NOT NULL CHECK (direction IN ('inbound', 'outbound', 'internal')),
    summary TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL CHECK (json_valid(metadata_json))
);

CREATE UNIQUE INDEX events_session_id_sequence ON events(session_id, sequence);
CREATE INDEX events_session_id_id_idx ON events(session_id, id);
-- Backs the status-message reader's latest-event-of-this-type lookup.
CREATE INDEX events_session_id_type_sequence_idx ON events(session_id, type, sequence);
CREATE INDEX events_repair_state_change_idx ON events(session_id, type, sequence)
WHERE (
    json_extract(metadata_json, '$.result') IN ('started', 'suspended')
    OR (json_extract(metadata_json, '$.result') = 'succeeded' AND json_extract(metadata_json, '$.attempt') = '0')
  );

-- heartbeat is the reactor's resettable quiet-backoff position; next_sequence is exclusive.
CREATE TABLE event_cursors (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('delivery', 'tick', 'heartbeat', 'forward')),
    next_sequence INTEGER NOT NULL CHECK (next_sequence >= 0),
    PRIMARY KEY (session_id, kind)
);

-- A resources row is released by the schema, not by each writer, so a write
-- that drops the last reference releases it whatever code made the write.
-- resource_references is the one list of those references; a new column that
-- refers to resources(id) joins it and gets its own pair of triggers, or a
-- release it does not know about fails that write on the foreign key.
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
