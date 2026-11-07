// Package persistence owns the SQLite database that backs core's durable
// runtime state, per docs/design/sqlite-persistence.md. It opens the
// database with a configurable journal mode (WAL by default; see
// JournalModeEnvVar), a bounded busy timeout, and foreign-key enforcement on
// every connection, applies the embedded goose migration history, and
// exposes a write-transaction helper that always starts with BEGIN
// IMMEDIATE so later slices never have to reason about a deferred
// transaction's read-then-write upgrade failure.
//
// The storage contract is package db (app/db). How to change the schema,
// regenerate the query code and migrations.sum, and add an upgrade fixture
// is in docs/design/sqlite-persistence.md under "Migration history".
package persistence

//go:generate go tool sqlc generate --file sqlc.yaml
//go:generate go run ../migrationsum ../../db
