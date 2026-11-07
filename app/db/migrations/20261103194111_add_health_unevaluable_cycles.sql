-- +goose Up
-- add column "health_unevaluable_cycles" to table: "sessions"
ALTER TABLE `sessions` ADD COLUMN `health_unevaluable_cycles` integer NULL;
