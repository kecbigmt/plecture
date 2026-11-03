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

-- +goose Down
-- reverse: add column "sleep_since" to table: "sessions"
ALTER TABLE `sessions` DROP COLUMN `sleep_since`;
