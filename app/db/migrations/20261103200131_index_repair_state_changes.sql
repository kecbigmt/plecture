-- +goose Up
-- create index "events_repair_state_change_idx" to table: "events"
CREATE INDEX `events_repair_state_change_idx` ON `events` (`session_id`, `type`, `sequence`) WHERE (
    json_extract(metadata_json, '$.result') IN ('started', 'suspended')
    OR (json_extract(metadata_json, '$.result') = 'succeeded' AND json_extract(metadata_json, '$.attempt') = '0')
  );
