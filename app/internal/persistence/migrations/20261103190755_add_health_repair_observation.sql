-- +goose Up
ALTER TABLE sessions ADD COLUMN health_repair_status TEXT CHECK (health_repair_status IS NULL OR health_repair_status IN ('repair_undecidable', 'repair_suspended'));
ALTER TABLE sessions ADD COLUMN health_repair_reason TEXT;
ALTER TABLE sessions ADD COLUMN health_unevaluable_cycles INTEGER;

-- +goose Down
ALTER TABLE sessions DROP COLUMN health_unevaluable_cycles;
ALTER TABLE sessions DROP COLUMN health_repair_reason;
ALTER TABLE sessions DROP COLUMN health_repair_status;
