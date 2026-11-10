package persistence

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	releasedV030Version = 20261108000000
	ownerSetupAt        = "2026-11-08T01:00:00.000000000Z"
)

func upTo(t *testing.T, db *DB, version int64) {
	t.Helper()
	provider, err := goose.NewProvider(goose.DialectSQLite3, db.write, db.migrations)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := provider.UpTo(context.Background(), version); err != nil {
		t.Fatalf("UpTo(%d): %v", version, err)
	}
}

func execAll(t *testing.T, db *DB, stmts ...string) {
	t.Helper()
	for _, stmt := range stmts {
		if _, err := db.write.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func scanOne(t *testing.T, db *DB, query string) string {
	t.Helper()
	got := queryStrings(t, context.Background(), db, query)
	if len(got) != 1 {
		t.Fatalf("%s returned %q, want one row", query, got)
	}
	return got[0]
}

// seedV030RowsWithEveryNullTheTighteningBackfills writes, at the v0.3.0
// schema, one row per backfill rule, plus a child of each rebuilt table that
// a cascading drop would delete.
func seedV030RowsWithEveryNullTheTighteningBackfills(t *testing.T, db *DB) {
	t.Helper()
	at := "'" + ownerSetupAt + "'"
	execAll(t, db,
		`INSERT INTO resources (id, type, name, locator, created_at) VALUES ('res-1', 'issue', 'issue-1', 'tracker:item/1', `+at+`)`,
		`INSERT INTO sessions (id, name, status, workflow, resource_id, health_last_state, health_notify_count, created_at, updated_at) VALUES ('s-live', 'live', 'up', 'wf', 'res-1', 'healthy', 3, `+at+`, `+at+`)`,
		`INSERT INTO sessions (id, name, status, workflow, health_last_reason, tick_last_fingerprint, created_at, updated_at) VALUES ('s-partial', 'partial', 'down', 'wf', 'quiet', 'fp', `+at+`, `+at+`)`,
		`INSERT INTO sessions (id, name, status, destroyed_at, workflow, inputs_json, created_at, updated_at) VALUES ('s-gone', 'gone', 'destroyed', `+at+`, 'wf', '{"k":"v"}', `+at+`, `+at+`)`,
		`INSERT INTO events (id, session_id, sequence, time, type, source, direction, summary, metadata_json) VALUES ('ev-1', 's-live', 1, `+at+`, 'user.note', 'cli', 'internal', 'kept', '{}')`,
		`INSERT INTO chain_attempts (session_id, instance, chain_id, fingerprint) VALUES ('s-live', 'initial', 'review', 'fp-1')`,
		`INSERT INTO node_instances (session_id, node_id) VALUES ('s-live', 'agent'), ('s-live', 'pane')`,
		`INSERT INTO node_executions (id, session_id, node_id, sequence, name, scope, status, done_when_json, setup_at) VALUES ('ne-agent', 's-live', 'agent', 1, 'stale', 'session', 'produced', 'null', `+at+`)`,
		`INSERT INTO node_executions (id, session_id, node_id, sequence, scope, status, outputs_json, setup_at) VALUES ('ne-pane', 's-live', 'pane', 1, 'session', 'produced', '{"pane":"1"}', `+at+`)`,
		`INSERT INTO node_execution_dependencies (execution_id, depends_on_execution_id) VALUES ('ne-agent', 'ne-pane')`,
		`INSERT INTO node_execution_layers (execution_id, position, effect_id, status) VALUES ('ne-agent', 0, 'terminal', 'produced')`,
		`INSERT INTO task_instances (id, session_id, instance_name, task_id, scope, status, sequence, named, setup_at) VALUES ('ti-1', 's-live', 'initial', 'work', 'session', 'produced', 1, 0, `+at+`)`,
		`INSERT INTO task_instance_layers (task_instance_id, position, effect_id, status, heartbeat_ticks) VALUES ('ti-1', 0, 'checkout', 'produced', 2)`,
		`INSERT INTO task_done_when_states (task_instance_id, last_action) VALUES ('ti-1', 'wait')`,
	)
}

func TestMigrate_TighteningBackfillsEveryNullItReplaces(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	upTo(t, db, releasedV030Version)
	seedV030RowsWithEveryNullTheTighteningBackfills(t, db)

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, check := range []rowCheck{
		{`SELECT name || '|' || inputs_json FROM sessions ORDER BY name`,
			[]string{`gone|{"k":"v"}`, "live|{}", "partial|{}"}},
		{`SELECT name || '|' || health_notify_count || '|' || health_unevaluable_cycles FROM sessions WHERE health_notify_count IS NOT NULL ORDER BY name`,
			[]string{"live|3|0", "partial|0|0"}},
		{`SELECT name FROM sessions WHERE health_notify_count IS NULL AND health_unevaluable_cycles IS NULL`,
			[]string{"gone"}},
		{`SELECT name || '|' || tick_consecutive_unchanged FROM sessions WHERE tick_consecutive_unchanged IS NOT NULL`,
			[]string{"partial|0"}},
		{`SELECT node_id || '|' || inputs_json || '|' || outputs_json || '|' || state_json || '|' || COALESCE(done_when_json, 'NULL') FROM node_executions ORDER BY node_id`,
			[]string{"agent|{}|{}|{}|NULL", `pane|{}|{"pane":"1"}|{}|NULL`}},
		{`SELECT effect_id || '|' || inputs_json || locals_json || outputs_json || env_json || '|' || heartbeat_ticks || '|' || heartbeat_escalations || '|' || setup_at FROM node_execution_layers`,
			[]string{"terminal|{}{}{}{}|0|0|" + ownerSetupAt}},
		{`SELECT instance_name || '|' || inputs_json || outputs_json || state_json FROM task_instances`,
			[]string{"initial|{}{}{}"}},
		{`SELECT effect_id || '|' || heartbeat_ticks || '|' || heartbeat_escalations || '|' || setup_at FROM task_instance_layers`,
			[]string{"checkout|2|0|" + ownerSetupAt}},
		{`SELECT id FROM events`, []string{"ev-1"}},
		{`SELECT fingerprint FROM chain_attempts`, []string{"fp-1"}},
		{`SELECT execution_id || '>' || depends_on_execution_id FROM node_execution_dependencies`, []string{"ne-agent>ne-pane"}},
		{`SELECT last_action FROM task_done_when_states`, []string{"wait"}},
		{`SELECT "table" FROM pragma_foreign_key_check`, nil},
	} {
		if got := queryStrings(t, ctx, db, check.query); !slices.Equal(got, check.want) {
			t.Errorf("%s\n got  %q\n want %q", check.query, got, check.want)
		}
	}
	if columns := queryStrings(t, ctx, db, `SELECT name FROM pragma_table_info('node_executions') WHERE name = 'name'`); len(columns) != 0 {
		t.Errorf("node_executions still has a name column")
	}

	// The release triggers account for both the session binding and its subscription.
	execAll(t, db, `DELETE FROM subscriptions WHERE session_id = 's-live'`)
	execAll(t, db, `UPDATE sessions SET resource_id = NULL WHERE id = 's-live'`)
	if n := scanOne(t, db, `SELECT count(*) FROM resources`); n != "0" {
		t.Errorf("resources after releasing the last reference = %s, want 0", n)
	}
}

func TestMigrate_TighteningRefusesALayerWhoseSetupTimeCannotBeBackfilled(t *testing.T) {
	for _, tc := range []struct {
		version   int64
		seed      []string
		nodeLayer string
	}{
		{
			version: firstReleasedVersion,
			seed: []string{
				`INSERT INTO node_instances (session_id, node_id, scope, status, sequence) VALUES ('s1', 'agent', 'session', 'produced', 1)`,
				`INSERT INTO node_instance_layers (session_id, node_id, position, effect_id, status) VALUES ('s1', 'agent', 0, 'terminal', 'produced'), ('s1', 'agent', 1, 'agent', 'produced')`,
				`INSERT INTO task_instances (id, session_id, instance_name, task_id, scope, status, sequence, named) VALUES ('ti-1', 's1', 'initial', 'work', 'session', 'produced', 1, 0)`,
			},
			nodeLayer: "node_instance_layers",
		},
		{
			version: releasedV030Version,
			seed: []string{
				`INSERT INTO node_instances (session_id, node_id) VALUES ('s1', 'agent')`,
				`INSERT INTO node_executions (id, session_id, node_id, sequence, scope, status) VALUES ('ne-1', 's1', 'agent', 1, 'session', 'produced')`,
				`INSERT INTO node_execution_layers (execution_id, position, effect_id, status) VALUES ('ne-1', 0, 'terminal', 'produced'), ('ne-1', 1, 'agent', 'produced')`,
				`INSERT INTO task_instances (id, session_id, instance_name, task_id, scope, status, sequence, named) VALUES ('ti-1', 's1', 'initial', 'work', 'session', 'produced', 1, 0)`,
			},
			nodeLayer: "node_execution_layers",
		},
	} {
		t.Run(fmt.Sprint(tc.version), func(t *testing.T) {
			db := openTestDB(t)
			ctx := context.Background()
			upTo(t, db, tc.version)
			now := "'" + ownerSetupAt + "'"
			execAll(t, db, `INSERT INTO sessions (id, name, status, workflow, created_at, updated_at) VALUES ('s1', 's1', 'down', 'wf', `+now+`, `+now+`)`)
			execAll(t, db, tc.seed...)
			execAll(t, db, `INSERT INTO task_instance_layers (task_instance_id, position, effect_id, status) VALUES ('ti-1', 0, 'checkout', 'produced')`)

			err := db.Migrate(ctx)
			if err == nil {
				t.Fatal("Migrate succeeded, want a refusal for layers with no setup time to copy")
			}
			for _, want := range []string{tc.nodeLayer + ".setup_at is NULL in 2 rows", "task_instance_layers.setup_at is NULL in 1 row", "docs/migrations/nullable-column-tightening.md"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to report %q", err, want)
				}
			}
			if current, err := db.Version(ctx); err != nil || current != tc.version {
				t.Errorf("version after the refusal = (%d, %v), want %d unchanged", current, err, tc.version)
			}
		})
	}
}

func TestMigrate_TighteningIgnoresLayersOfWorkflowNodesAnEarlierMigrationDeletes(t *testing.T) {
	for _, version := range []int64{firstReleasedVersion, 20261104014706} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			db := seedWorkflowNodeAt(t, version, "destroyed", "produced")
			layer := `INSERT INTO node_execution_layers (execution_id, position, effect_id, status) VALUES ('exec-workflow', 0, 'workspace', 'produced')`
			if version < nodeExecutionsVersion {
				layer = `INSERT INTO node_instance_layers (session_id, node_id, position, effect_id, status) VALUES ('s1', '@workflow', 0, 'workspace', 'produced')`
			}
			execAll(t, db, layer)

			if err := db.Migrate(context.Background()); err != nil {
				t.Fatalf("Migrate: %v", err)
			}
		})
	}
}
