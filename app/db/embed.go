// Package db is the storage contract: the declarative schema, the query
// definitions sqlc compiles, and the forward-only migration history with
// its integrity manifest. The code that verifies those files lives here
// too, apart from the code that uses the database, so a change that weakens
// a check passes the same owner gate as a change to the files it guards.
package db

import "embed"

// Migrations holds migrations/*.sql, the history goose applies.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Schema is schema.sql, the declarative structure the migration history
// must reproduce.
//
//go:embed schema.sql
var Schema string
