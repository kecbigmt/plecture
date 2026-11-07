---
supersedes: 2026-09-06-sqlite-durable-storage
---

# Forward-only goose migrations verified by Go tests instead of Atlas

## Context

`2026-09-06-sqlite-durable-storage` made `schema.sql` the declarative
authority, used Atlas Community Edition to generate goose migrations from it,
and relied on Atlas's `atlas.sum` to protect the migration files. CI fetched
a pinned Atlas binary (v1.3.0), regenerated the migration history, and failed
on a diff. goose applies the migrations at runtime.

The work leading to the breaking release that removed the workspace provider
measured what that pipeline cost:

- Atlas Community cannot carry triggers. The release of a `resources` row
  that no longer has a referrer is implemented in Go instead of in the
  schema.
- `atlas.sum` must be recomputed after every edit. A hash computed before a
  later comment edit failed CI once, and in-place edits of unreleased
  migrations on the integration branch fought the manifest's aggregate line
  on every rebase.
- Most SQLite migrations are hand-written anyway: a key change is a table
  rebuild that Atlas cannot diff safely against a table holding rows, so the
  diff generator rarely applied.
- Every developer and CI run needed an external binary, pinned by URL and
  checksum per platform.

The migrations written so far also carried `-- +goose Down` sections, and
none of them was a verified inverse: the Down of the migration that records
each resource once does not restore `resource_type`, and the Down of the
migration that identifies populations by project root keeps only the
global-context populations. goose v3.27 runs a migration with no Down
section as an empty migration: `down` deletes its ledger entry and leaves
the schema unchanged.

## Supersession scope

- **Declarative authority and migration tooling:** generating goose
  migrations from `schema.sql` with Atlas Community Edition, pinning the
  Community Edition distribution, avoiding objects Community Edition
  excludes such as views and triggers, and CI's reproducible-generation check
  of the migration history are replaced by the decisions below. The
  reproducible-generation check of sqlc's output is not replaced.
- **Upgrade and recovery responsibilities:** "restoring a pre-migration
  backup discards subsequent updates unless separately preserved" as the
  recovery path is replaced by the rollback policy below.

## Decision

### Source of truth and migrations

`schema.sql` stays the declarative source of truth for the database
structure. Migrations are hand-written goose SQL, applied by goose, whose
ledger stays the only record of which migrations have run. Atlas is no
longer used for anything.

Migrations are forward-only. No migration has a `-- +goose Down` section,
and the existing sections are deleted. A migration that has shipped never
changes. The Down sections deleted by this decision are the one exception;
no Up section changes.

### Verification

Three Go tests replace Atlas's checks:

- The existing drift test, `TestSchemaSQL_MatchesMigrationHistory`, applies
  every migration to an empty database and compares the result with
  `schema.sql` applied directly. Tables keep their comparison through SQLite
  introspection. Triggers and views are compared by `(type, name)`, their
  table, and their `CREATE` text normalized for comments, identifier quoting,
  whitespace, and case outside string literals.
- A checked-in `migrations.sum` holds the SHA-256 of every migration file in
  `sha256sum` format, sorted by name, with no aggregate line. A Go test fails
  on any edit it does not record, released or not, and on any migration with
  a Down section. The author of a migration change rewrites it with
  `go test ./db -update` and commits it with the change; CI and reviewers
  only verify.
- One fixture per released schema version, a `sqlite3` dump built from that
  release tag's own migration files plus representative rows, is upgraded
  with the current migrations under `-tags integration`. The test asserts the
  resulting version and the rows that survive.

### Location

The storage contract lives in `app/db/`: `schema.sql`, `query.sql`,
`migrations/`, `migrations.sum`, and an `embed.go` that exposes the
migrations and the schema text. `sqlc.yaml`, the generated code, and the Go
code that uses the database stay in `app/internal/persistence`.

### Rollback

A problem found after an upgrade is fixed forward, with a new migration.
Restoring the backup taken before the upgrade is allowed only right after the
upgrade, before anything has been written to the new schema. There is no
other rollback path. Development resets use a scratch `PLECT_DATA_HOME`.

## Consequences

- `plect` has no down path. Running the goose CLI's `down` by hand against a
  store deletes a ledger entry without changing the schema, so the ledger
  then describes a schema the store does not have. The documentation says it
  must never be used.
- A migration edit costs one `go test ./db -update`; the release procedure
  has no manifest step. A release with a new schema version adds one upgrade
  fixture, following the recipe in the persistence package documentation.
- Two migration PRs no longer conflict on the manifest unless they edit the
  same migration.
- Triggers and views are available to the schema, because the drift test
  covers them. Moving the `resources` row release into a trigger is left to
  the next change that touches `resources`.
- The owner-gated path list moves from `app/internal/persistence/schema.sql`,
  `migrations/`, and `atlas.sum` to `app/db/**`.
- Without a diff generator, a migration's author writes the SQL that reaches
  `schema.sql`; the drift test tells them when it does not.
- Removal condition: revisit this decision if a schema difference the drift
  test missed reaches a release, or if writing migrations by hand without a
  diff generator causes a released mistake.

## Alternatives considered

### Keep Atlas as is

This is rejected because of the costs measured in Context: no triggers, the
`atlas.sum` friction with in-place edits of unreleased migrations, a diff
generator that rarely applies to SQLite rebuilds, and an external binary pin.

### Atlas's paid tier

It carries triggers, but it makes a billing dependency of a single-user
project's schema feature.

### `PRAGMA user_version` plus Go upgrade functions

The upgrade SQL would be scattered in Go strings, which are harder to review
and to compare with `schema.sql` than migration files.

### golang-migrate or dbmate

They are equivalent to goose for applying migrations and have no diff
either. Switching costs effort and buys nothing.

### sqldef (`sqlite3def`)

It gives a declarative diff, but like Atlas it cannot safely rebuild a table
that holds rows, which is the common SQLite migration here.

### A verified lossless Down for every migration

Each migration would need an inverse mapping designed and tested. The Downs
written so far were already lossy. Sessions created after an upgrade pin
workflow revisions in the new dialect, which the previous binary cannot run,
so keeping post-upgrade writes across a rollback has little value.

### Keep the unverified Down sections

An unverified rollback is the same hazard as an unverified backup: it is
trusted at the moment it is needed and fails then.
