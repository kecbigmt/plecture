-- +goose Up
-- add column "sleep_since" to table: "sessions"
ALTER TABLE `sessions` ADD COLUMN `sleep_since` text NULL;
UPDATE sessions
SET sleep_since = (
    SELECT e.time FROM events e
    WHERE e.session_id = sessions.id
      AND e.type = 'plect.workflow_population.down'
      AND json_extract(e.metadata_json, '$.reason') = 'capacity'
      AND e.sequence > COALESCE((
          SELECT MAX(d.sequence) FROM events d
          WHERE d.session_id = sessions.id AND d.type = 'lifecycle.down'
      ), 0)
    ORDER BY e.sequence DESC LIMIT 1
)
WHERE status = 'down'
  AND EXISTS (
    SELECT 1 FROM events e
    WHERE e.session_id = sessions.id
      AND e.type = 'plect.workflow_population.down'
      AND json_extract(e.metadata_json, '$.reason') = 'capacity'
      AND e.sequence > COALESCE((
          SELECT MAX(d.sequence) FROM events d
          WHERE d.session_id = sessions.id AND d.type = 'lifecycle.down'
      ), 0)
  );

INSERT INTO events (id, session_id, sequence, time, type, source, direction, summary, body, metadata_json)
SELECT 'migration-stop-' || s.id, s.id,
       COALESCE((SELECT MAX(e.sequence) FROM events e WHERE e.session_id = s.id), 0) + 1,
       strftime('%Y-%m-%dT%H:%M:%f', 'now') || '000000Z',
       'plect.session.stop', 'plect', 'internal', 'session fully stopped', '', '{"origin":"migration"}'
FROM sessions s
WHERE s.status = 'down' AND s.sleep_since IS NULL;
