// Package persistence owns the SQLite database that backs core's durable
// runtime state, per docs/design/sqlite-persistence.md. It opens the
// database with a configurable journal mode (WAL by default; see
// JournalModeEnvVar), a bounded busy timeout, and foreign-key enforcement on
// every connection, applies the embedded goose migration history, and
// exposes a write-transaction helper that always starts with BEGIN
// IMMEDIATE so later slices never have to reason about a deferred
// transaction's read-then-write upgrade failure.
//
// The storage contract lives in package db (app/db): schema.sql,
// query.sql, the migrations/ history, and migrations.sum. Runtime state
// tables (sessions, task/node instances, done_when/judge state,
// populations, up-slot reservations) live in schema.sql; the migration
// history also records a since-retired persistence_smoke table that once
// proved the schema.sql -> goose -> SQLite and schema.sql -> sqlc -> Go
// pipelines end to end before any domain table existed to prove them
// instead.
//
// # Changing the schema
//
// Edit app/db/schema.sql, then add a hand-written goose migration under
// app/db/migrations/ that reaches the same structure. A migration has only
// an Up section: migrations are forward-only, and a problem found after an
// upgrade is fixed with a new migration. A migration that has shipped in a
// release never changes. TestSchemaSQL_MatchesMigrationHistory proves the
// migration history and schema.sql agree; TestMigrationsMatchTheirManifest
// (internal/migrationsum) fails on any edit migrations.sum does not record
// and on any Down section.
//
// # Regenerating
//
// Query code (app/db's schema.sql + query.sql -> sqlcgen) and
// app/db/migrations.sum:
//
//	go generate ./internal/persistence/...
//
// # Adding an upgrade fixture
//
// A release whose schema version is new adds testdata/upgrade/<version>.sql,
// a dump of a store built from that release tag's own migration files, and
// lists the rows its upgrade must leave in upgradeFixtures. <migrations> is
// the tag's migrations directory: app/db/migrations, or
// app/internal/persistence/migrations in a tag older than the db package.
//
//	git archive <tag> <migrations> | tar -x -C <dir>
//	cd <dir>
//	go run github.com/pressly/goose/v3/cmd/goose@<go.mod version> \
//	    -dir <migrations> sqlite3 store.db up
//	sqlite3 store.db < <representative rows, adapted from the previous fixture>
//	sqlite3 store.db .dump > <app>/internal/persistence/testdata/upgrade/<version>.sql
package persistence

//go:generate go tool sqlc generate --file sqlc.yaml
//go:generate go run ../migrationsum ../../db
