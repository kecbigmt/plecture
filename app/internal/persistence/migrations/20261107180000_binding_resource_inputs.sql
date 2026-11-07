-- +goose Up
-- Resource action inputs supplied by a binding: the entry binding's object
-- on the session, each task binding's object on its instance, and the
-- queuing binding's object on a delivery retry. Existing rows keep NULL,
-- which reads as the empty object every binding received before inputs
-- could be supplied.
ALTER TABLE sessions ADD COLUMN resource_inputs_json TEXT CHECK (resource_inputs_json IS NULL OR json_valid(resource_inputs_json));
ALTER TABLE task_instances ADD COLUMN resource_inputs_json TEXT CHECK (resource_inputs_json IS NULL OR json_valid(resource_inputs_json));
ALTER TABLE subscription_retries ADD COLUMN inputs_json TEXT CHECK (inputs_json IS NULL OR json_valid(inputs_json));

-- +goose Down
-- Dropping the columns discards every recorded binding's action inputs.
ALTER TABLE subscription_retries DROP COLUMN inputs_json;
ALTER TABLE task_instances DROP COLUMN resource_inputs_json;
ALTER TABLE sessions DROP COLUMN resource_inputs_json;
