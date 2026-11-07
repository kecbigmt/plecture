// Package db is the storage contract: the declarative schema, the query
// definitions sqlc compiles, and the forward-only migration history with
// its integrity manifest. It holds no Go logic, so the contract's files can
// be owner-gated as one directory apart from the code that uses them.
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
