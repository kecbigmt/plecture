-- +goose Up
-- A live session whose workspace provider has not been released owns an
-- allocation only the previous release can free, so the upgrade refuses
-- instead of stranding it. The failing constraint's name is the instruction.
CREATE TEMP TABLE workspace_provider_sessions_block_upgrade (
    session_name TEXT NOT NULL,
    CONSTRAINT "destroy live sessions that still hold a workspace provider with the previous release, then upgrade; see docs/migrations/resource-and-workspace-effects-migration.md" CHECK (0)
);
INSERT INTO workspace_provider_sessions_block_upgrade (session_name)
SELECT DISTINCT s.name
FROM node_executions ne
JOIN sessions s ON s.id = ne.session_id
WHERE ne.node_id = '@workflow' AND ne.status <> 'cleaned' AND s.status <> 'destroyed';
DROP TABLE workspace_provider_sessions_block_upgrade;

DELETE FROM node_executions WHERE node_id = '@workflow';
DELETE FROM node_instances WHERE node_id = '@workflow';

ALTER TABLE sessions DROP COLUMN workspace_dir;
ALTER TABLE node_executions ADD COLUMN directory TEXT;
ALTER TABLE task_instances ADD COLUMN directory TEXT;
