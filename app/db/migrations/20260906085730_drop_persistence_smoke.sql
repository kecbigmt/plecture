-- +goose Up
-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- drop "persistence_smoke" table
DROP TABLE `persistence_smoke`;
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
