-- +goose Up
ALTER TABLE resources ADD COLUMN max_subscribers INTEGER CHECK (max_subscribers IS NULL OR max_subscribers > 0);
CREATE TABLE subscriptions (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    resource_id TEXT NOT NULL REFERENCES resources(id),
    source TEXT NOT NULL CHECK (source IN ('runtime', 'dispatch', 'task', 'node')),
    inputs_json TEXT NOT NULL CHECK (json_valid(inputs_json) AND json_type(inputs_json) = 'object'),
    created_at TEXT NOT NULL,
    PRIMARY KEY (session_id, resource_id, source)
);
CREATE INDEX subscriptions_resource_idx ON subscriptions(resource_id);
DROP VIEW resource_references;
CREATE VIEW resource_references AS
SELECT resource_id FROM sessions WHERE resource_id IS NOT NULL
UNION ALL SELECT resource_id FROM task_instances WHERE resource_id IS NOT NULL
UNION ALL SELECT resource_id FROM node_executions WHERE resource_id IS NOT NULL
UNION ALL SELECT subscription_id FROM node_executions WHERE subscription_id IS NOT NULL
UNION ALL SELECT resource_id FROM population_members
UNION ALL SELECT resource_id FROM subscriptions
UNION ALL SELECT resource_id FROM subscription_retries;
-- +goose StatementBegin
CREATE TRIGGER subscriptions_release_resource AFTER DELETE ON subscriptions BEGIN
    DELETE FROM resources WHERE id = OLD.resource_id AND NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = OLD.resource_id);
END;
-- +goose StatementEnd
INSERT INTO subscriptions (session_id, resource_id, source, inputs_json, created_at)
SELECT id, resource_id, 'dispatch', '{}', created_at FROM sessions
WHERE status <> 'destroyed' AND resource_id IS NOT NULL
ON CONFLICT DO NOTHING;
INSERT INTO subscriptions (session_id, resource_id, source, inputs_json, created_at)
SELECT DISTINCT t.session_id, t.resource_id, 'task', '{}', s.created_at
FROM task_instances t JOIN sessions s ON s.id = t.session_id
WHERE s.status <> 'destroyed' AND t.status <> 'cleaned' AND t.resource_id IS NOT NULL
ON CONFLICT DO NOTHING;
INSERT INTO subscriptions (session_id, resource_id, source, inputs_json, created_at)
SELECT DISTINCT n.session_id, n.subscription_id, 'node', '{}', s.created_at
FROM node_executions n JOIN sessions s ON s.id = n.session_id
WHERE s.status <> 'destroyed' AND n.status <> 'cleaned' AND n.subscription_id IS NOT NULL
ON CONFLICT DO NOTHING;
INSERT INTO subscriptions (session_id, resource_id, source, inputs_json, created_at)
SELECT r.session_id, r.resource_id, 'runtime', '{}', s.created_at
FROM subscription_retries r JOIN sessions s ON s.id = r.session_id
WHERE s.status <> 'destroyed' AND r.action = 'subscribe'
ON CONFLICT DO NOTHING;
DROP TRIGGER subscription_retries_release_resource;
DROP TRIGGER subscription_retries_release_replaced_resource;
DROP VIEW resource_references;
DROP TABLE subscription_retries;
CREATE VIEW resource_references AS
SELECT resource_id FROM sessions WHERE resource_id IS NOT NULL
UNION ALL SELECT resource_id FROM task_instances WHERE resource_id IS NOT NULL
UNION ALL SELECT resource_id FROM node_executions WHERE resource_id IS NOT NULL
UNION ALL SELECT subscription_id FROM node_executions WHERE subscription_id IS NOT NULL
UNION ALL SELECT resource_id FROM population_members
UNION ALL SELECT resource_id FROM subscriptions;
DELETE FROM resources WHERE NOT EXISTS (SELECT 1 FROM resource_references WHERE resource_id = resources.id);
