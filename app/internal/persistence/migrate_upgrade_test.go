package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/kecbigmt/plecture/contracts/event"
)

// eventTablesVersion is the goose version immediately before this package's
// events.delivery_mode column drop — the last migration where a row can
// still be inserted with that column present.
const eventTablesVersion = 20260906143514

func TestMigrate_CapacityDownBecomesSleepAndManualDownStaysStopped(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	provider, err := goose.NewProvider(goose.DialectSQLite3, db.write, db.migrations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 20261103194111); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"capacity", "manual"} {
		if _, err := db.write.ExecContext(ctx, `INSERT INTO sessions(id,name,status,workflow,created_at,updated_at) VALUES(?,?, 'down','wf',?,?)`, name, name, "2026-11-03T10:00:00.000000000Z", "2026-11-03T10:00:00.000000000Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.write.ExecContext(ctx, `INSERT INTO events(id,session_id,sequence,time,type,source,direction,summary,body,metadata_json) VALUES(?,?,1,?,'lifecycle.up','plect','internal','','','{}')`, name+"-up", name, "2026-11-03T10:00:30.000000000Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.write.ExecContext(ctx, `INSERT INTO events(id,session_id,sequence,time,type,source,direction,summary,body,metadata_json) VALUES(?,?,2,?,'lifecycle.down','plect','internal','','','{}')`, name+"-down", name, "2026-11-03T10:01:00.000000000Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.write.ExecContext(ctx, `INSERT INTO events(id,session_id,sequence,time,type,source,direction,summary,body,metadata_json) VALUES('cap-event','capacity',3,?,'plect.workflow_population.down','plect','internal','','','{"reason":"capacity"}')`, "2026-11-03T10:02:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		sleeping bool
	}{{"capacity", true}, {"manual", false}} {
		var since sql.NullString
		if err := db.write.QueryRowContext(ctx, `SELECT sleep_since FROM sessions WHERE name=?`, tc.name).Scan(&since); err != nil {
			t.Fatal(err)
		}
		if since.Valid != tc.sleeping {
			t.Errorf("%s sleep_since = %+v, want sleeping=%t", tc.name, since, tc.sleeping)
		}
		events, _, err := db.ListEventsFrom(ctx, tc.name, 0)
		if err != nil {
			t.Fatal(err)
		}
		stops := 0
		latestIntent := ""
		for _, ev := range events {
			if ev.Type == event.TypeLifecyclePrefix+"up" || ev.Type == event.TypeSessionStop {
				latestIntent = ev.Type
			}
			if ev.Type == event.TypeSessionStop {
				stops++
				if ev.Metadata["origin"] != "migration" {
					t.Fatalf("%s stop event = %+v, want migration origin", tc.name, ev)
				}
			}
		}
		if tc.sleeping && stops != 0 || !tc.sleeping && stops != 1 {
			t.Errorf("%s stop events = %d, sleeping=%t", tc.name, stops, tc.sleeping)
		}
		if !tc.sleeping && latestIntent != event.TypeSessionStop {
			t.Errorf("%s latest lifecycle intent = %q, want %q", tc.name, latestIntent, event.TypeSessionStop)
		}
	}
}

// seedEventRow uses raw SQL rather than AppendEvent: AppendEvent's generated
// insert targets only the current, already-migrated column set.
func seedEventRow(t *testing.T, db *DB, ctx context.Context, streamID, session, eventID string, when time.Time, withDeliveryMode bool) {
	t.Helper()
	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO event_streams (id, session_name, created_at) VALUES (?, ?, ?)`,
		streamID, session, formatTime(when),
	); err != nil {
		t.Fatalf("seed event_streams: %v", err)
	}
	columns := "id, stream_id, sequence, time, type, source, direction, summary, body, metadata_json"
	args := []any{eventID, streamID, formatTime(when), "widget.message", "widget", "inbound", "hello", "hello body", `{"k":"v"}`}
	if withDeliveryMode {
		columns += ", delivery_mode"
		args = append(args, "push")
	}
	placeholders := "?, ?, 1, ?, ?, ?, ?, ?, ?, ?"
	if withDeliveryMode {
		placeholders += ", ?"
	}
	if _, err := db.write.ExecContext(ctx,
		"INSERT INTO events ("+columns+") VALUES ("+placeholders+")", args...,
	); err != nil {
		t.Fatalf("seed event row: %v", err)
	}
}

// assertEventRowIntact checks every retained field, not just id and type: a
// column-drop migration that silently truncated or reordered one would
// otherwise pass unnoticed.
func assertEventRowIntact(t *testing.T, db *DB, ctx context.Context, session, eventID string, when time.Time) {
	t.Helper()
	evs, seqs, err := db.ListEventsFrom(ctx, session, 0)
	if err != nil {
		t.Fatalf("ListEventsFrom: %v", err)
	}
	var got event.Event
	sequence := int64(0)
	for i, ev := range evs {
		if ev.ID == eventID {
			got = ev
			sequence = seqs[i]
		}
	}
	if got.ID == "" {
		t.Fatalf("event %q missing after migration", eventID)
	}
	if got.ID != eventID || got.Type != "widget.message" || got.Source != "widget" ||
		string(got.Direction) != "inbound" || got.Summary != "hello" || got.Body != "hello body" {
		t.Fatalf("surviving columns = %+v, want the seeded values intact", got)
	}
	if !got.Time.Equal(when) {
		t.Errorf("Time = %v, want %v", got.Time, when)
	}
	if got.Metadata["k"] != "v" {
		t.Errorf("Metadata = %v, want k=v", got.Metadata)
	}
	if sequence != 1 {
		t.Fatalf("sequence = %d, want 1", sequence)
	}
}

func deliveryModeColumnCount(t *testing.T, db *DB, ctx context.Context) int {
	t.Helper()
	var count int
	if err := db.write.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = 'delivery_mode'`,
	).Scan(&count); err != nil {
		t.Fatalf("check delivery_mode column: %v", err)
	}
	return count
}

// TestMigrate_DropsDeliveryModeColumnWithoutLosingExistingRows is the
// upgrade-path case TestSchemaSQL_MatchesMigrationHistory cannot cover: that
// check only ever migrates an empty database.
func TestMigrate_DropsDeliveryModeColumnWithoutLosingExistingRows(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	when := time.Now().UTC().Truncate(time.Second)

	provider, err := goose.NewProvider(goose.DialectSQLite3, db.write, db.migrations)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := provider.UpTo(ctx, eventTablesVersion); err != nil {
		t.Fatalf("UpTo(%d): %v", eventTablesVersion, err)
	}
	seedEventRow(t, db, ctx, "01STREAM0000000000000000", "s1", "01EVENT0000000000000000", when, true)

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate to current: %v", err)
	}

	if got := deliveryModeColumnCount(t, db, ctx); got != 0 {
		t.Fatalf("events.delivery_mode still present after migration")
	}
	assertEventRowIntact(t, db, ctx, "s1", "01EVENT0000000000000000", when)
}

// preNodeExecutionIdentityVersion is the last migration where a
// node_instances row still carries its own status/inputs/outputs directly.
const preNodeExecutionIdentityVersion = 20260907002408

// Covers the upgrade path TestSchemaSQL_MatchesMigrationHistory cannot: that
// check only ever migrates an empty database.
func TestMigrate_AddNodeExecutionIdentityPreservesExistingNodeRows(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	provider, err := goose.NewProvider(goose.DialectSQLite3, db.write, db.migrations)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := provider.UpTo(ctx, preNodeExecutionIdentityVersion); err != nil {
		t.Fatalf("UpTo(%d): %v", preNodeExecutionIdentityVersion, err)
	}

	sessionID := "01SESSION000000000000000"
	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO sessions (id, name, status, workflow, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, "s1", "up", "wf", formatTime(now), formatTime(now),
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO node_instances (session_id, node_id, task_id, scope, status, sequence, inputs_json, outputs_json, error, setup_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, "n1", "work", "session", "failed", 1, `{"a":1}`, `{"b":2}`, "boom", formatTime(now),
	); err != nil {
		t.Fatalf("seed node_instances row: %v", err)
	}
	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO node_instance_layers (session_id, node_id, position, effect_id, status, inputs_json)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, "n1", 0, "eff1", "failed", `{"c":3}`,
	); err != nil {
		t.Fatalf("seed node_instance_layers row: %v", err)
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("UpByOne (apply node-execution-identity): %v", err)
	}

	var execID, taskID, scope, status, inputsJSON, outputsJSON, errCol string
	if err := db.write.QueryRowContext(ctx,
		`SELECT id, task_id, scope, status, inputs_json, outputs_json, error FROM node_executions WHERE session_id = ? AND node_id = ?`,
		sessionID, "n1",
	).Scan(&execID, &taskID, &scope, &status, &inputsJSON, &outputsJSON, &errCol); err != nil {
		t.Fatalf("read migrated node_executions row: %v", err)
	}
	if taskID != "work" || scope != "session" || status != "failed" || inputsJSON != `{"a":1}` || outputsJSON != `{"b":2}` || errCol != "boom" {
		t.Fatalf("migrated execution = (%q,%q,%q,%q,%q,%q), want the seeded node_instances values intact", taskID, scope, status, inputsJSON, outputsJSON, errCol)
	}

	var layerEffectID, layerStatus, layerInputsJSON string
	if err := db.write.QueryRowContext(ctx,
		`SELECT effect_id, status, inputs_json FROM node_execution_layers WHERE execution_id = ? AND position = 0`,
		execID,
	).Scan(&layerEffectID, &layerStatus, &layerInputsJSON); err != nil {
		t.Fatalf("read migrated node_execution_layers row: %v", err)
	}
	if layerEffectID != "eff1" || layerStatus != "failed" || layerInputsJSON != `{"c":3}` {
		t.Fatalf("migrated layer = (%q,%q,%q), want the seeded node_instance_layers values intact", layerEffectID, layerStatus, layerInputsJSON)
	}
}

// preWorkdirDirectoryVersion is the last migration before sessions.workspace_dir
// is dropped and the @workflow rows are deleted.
const preWorkdirDirectoryVersion = 20261104140000

// workdirMigrationFixture opens a database at the version before the
// workdir migration and seeds one session with a workspace_dir, a retired
// @workflow node execution of the given status, and an ordinary node.
func workdirMigrationFixture(t *testing.T, sessionStatus, workflowNodeStatus string) (*DB, *goose.Provider, string) {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	now := formatTime(time.Now().UTC())

	provider, err := goose.NewProvider(goose.DialectSQLite3, db.write, db.migrations)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := provider.UpTo(ctx, preWorkdirDirectoryVersion); err != nil {
		t.Fatalf("UpTo(%d): %v", preWorkdirDirectoryVersion, err)
	}

	var destroyedAt sql.NullString
	if sessionStatus == "destroyed" {
		destroyedAt = sql.NullString{String: now, Valid: true}
	}
	sessionID := "01SESSION000000000000000"
	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO sessions (id, name, status, destroyed_at, workflow, workspace_dir, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, "s1", sessionStatus, destroyedAt, "wf", "/tmp/old-workspace", now, now,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	for _, nodeID := range []string{"@workflow", "n1"} {
		if _, err := db.write.ExecContext(ctx,
			`INSERT INTO node_instances (session_id, node_id) VALUES (?, ?)`, sessionID, nodeID,
		); err != nil {
			t.Fatalf("seed node_instances %q: %v", nodeID, err)
		}
	}
	status := map[string]string{"@workflow": workflowNodeStatus, "n1": "produced"}
	for nodeID, st := range status {
		if _, err := db.write.ExecContext(ctx,
			`INSERT INTO node_executions (id, session_id, node_id, sequence, scope, status) VALUES (?, ?, ?, 1, 'session', ?)`,
			"exec-"+nodeID, sessionID, nodeID, st,
		); err != nil {
			t.Fatalf("seed node_executions %q: %v", nodeID, err)
		}
	}
	return db, provider, sessionID
}

func sessionsHasColumn(t *testing.T, db *DB, column string) bool {
	t.Helper()
	var n int
	if err := db.write.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = ?`, column).Scan(&n); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	return n > 0
}

func countExecutions(t *testing.T, db *DB, nodeID string) int {
	t.Helper()
	var n int
	if err := db.write.QueryRow(`SELECT COUNT(*) FROM node_executions WHERE node_id = ?`, nodeID).Scan(&n); err != nil {
		t.Fatalf("count node_executions: %v", err)
	}
	return n
}

func TestMigrate_WorkdirDirectoryRefusesWhileALiveSessionHoldsAnUnreleasedWorkflowNode(t *testing.T) {
	for _, workflowNodeStatus := range []string{"produced", "failed"} {
		t.Run(workflowNodeStatus, func(t *testing.T) {
			db, provider, _ := workdirMigrationFixture(t, "up", workflowNodeStatus)

			_, err := provider.UpByOne(context.Background())
			if err == nil {
				t.Fatal("migration succeeded, want a refusal while a live session holds an unreleased @workflow node")
			}
			if !strings.Contains(err.Error(), workdirDirectoryRefusal) {
				t.Errorf("error = %q, want the workdir-directory refusal %q", err, workdirDirectoryRefusal)
			}
			if !sessionsHasColumn(t, db, "workspace_dir") {
				t.Error("sessions.workspace_dir was dropped by a refused migration")
			}
			if got := countExecutions(t, db, "@workflow"); got != 1 {
				t.Errorf("@workflow node_executions rows = %d after a refused migration, want 1", got)
			}
		})
	}
}

func TestMigrate_WorkdirDirectoryDropsWorkflowRowsAndWorkspaceColumnOtherwise(t *testing.T) {
	tests := []struct {
		name               string
		sessionStatus      string
		workflowNodeStatus string
	}{
		{"released workflow node of a live session", "up", "cleaned"},
		{"unreleased workflow node of a destroyed session", "destroyed", "produced"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, provider, sessionID := workdirMigrationFixture(t, tc.sessionStatus, tc.workflowNodeStatus)
			ctx := context.Background()

			if _, err := provider.UpByOne(ctx); err != nil {
				t.Fatalf("UpByOne (workdir migration): %v", err)
			}

			if got := countExecutions(t, db, "@workflow"); got != 0 {
				t.Errorf("@workflow node_executions rows = %d, want 0", got)
			}
			var instances int
			if err := db.write.QueryRow(`SELECT COUNT(*) FROM node_instances WHERE node_id = '@workflow'`).Scan(&instances); err != nil {
				t.Fatalf("count node_instances: %v", err)
			}
			if instances != 0 {
				t.Errorf("@workflow node_instances rows = %d, want 0", instances)
			}
			if sessionsHasColumn(t, db, "workspace_dir") {
				t.Error("sessions.workspace_dir still exists")
			}

			var status string
			var directory sql.NullString
			if err := db.write.QueryRow(
				`SELECT status, directory FROM node_executions WHERE session_id = ? AND node_id = 'n1'`, sessionID,
			).Scan(&status, &directory); err != nil {
				t.Fatalf("read the ordinary node's execution: %v", err)
			}
			if status != "produced" || directory.Valid {
				t.Errorf("ordinary node = (%q, directory %v), want it intact with a NULL directory", status, directory)
			}
			var name string
			if err := db.write.QueryRow(`SELECT name FROM sessions WHERE id = ?`, sessionID).Scan(&name); err != nil || name != "s1" {
				t.Errorf("session row = (%q, %v), want it intact", name, err)
			}
		})
	}
}

func TestMigrate_InvocationDirLeavesExistingSessionsWithNoRecordedContext(t *testing.T) {
	db, provider, sessionID := workdirMigrationFixture(t, "up", "cleaned")
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := provider.UpByOne(ctx); err != nil {
			t.Fatalf("UpByOne %d: %v", i, err)
		}
	}

	var dir sql.NullString
	if err := db.write.QueryRow(`SELECT invocation_dir FROM sessions WHERE id = ?`, sessionID).Scan(&dir); err != nil {
		t.Fatalf("read invocation_dir: %v", err)
	}
	if dir.Valid {
		t.Errorf("invocation_dir = %q for a pre-existing session, want NULL", dir.String)
	}
}

func TestMigrate_EntryResourceLeavesExistingSessionsUnrecorded(t *testing.T) {
	db, provider, sessionID := workdirMigrationFixture(t, "up", "cleaned")
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := provider.UpByOne(ctx); err != nil {
			t.Fatalf("UpByOne %d: %v", i, err)
		}
	}

	var resourceType, tag sql.NullString
	if err := db.write.QueryRow(`SELECT resource_type, tag FROM sessions WHERE id = ?`, sessionID).Scan(&resourceType, &tag); err != nil {
		t.Fatalf("read entry columns: %v", err)
	}
	if resourceType.Valid || tag.Valid {
		t.Errorf("entry = (%v, %v) for a pre-existing session, want both NULL", resourceType, tag)
	}
}

const preResourcesVersion = 20261107120000

func TestMigrate_ResourcesRecordEveryExistingIdentifierOnceAndRepointEachReference(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	provider, err := goose.NewProvider(goose.DialectSQLite3, db.write, db.migrations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, preResourcesVersion); err != nil {
		t.Fatal(err)
	}
	now := formatTime(time.Now().UTC())
	seed := []string{
		`INSERT INTO sessions (id, name, status, resource_id, resource_type, tag, workflow, created_at, updated_at) VALUES ('s-live', 'case-1+review', 'up', 'case://1', 'case', 'review', 'wf', '` + now + `', '` + now + `')`,
		`INSERT INTO sessions (id, name, status, destroyed_at, resource_id, workflow, created_at, updated_at) VALUES ('s-gone', 'case-1+wf', 'destroyed', '` + now + `', 'case://1', 'wf', '` + now + `', '` + now + `')`,
		`INSERT INTO sessions (id, name, status, workflow, created_at, updated_at) VALUES ('s-bare', 'bare', 'down', 'wf', '` + now + `', '` + now + `')`,
		`INSERT INTO task_instances (id, session_id, instance_name, task_id, scope, status, sequence, resource, named) VALUES ('ti-1', 's-live', 'initial', 'work', 'session', 'produced', 1, 'case://2', 1)`,
		`INSERT INTO node_instances (session_id, node_id) VALUES ('s-live', 'n1')`,
		`INSERT INTO node_executions (id, session_id, node_id, sequence, scope, status, resource, subscription) VALUES ('ne-1', 's-live', 'n1', 1, 'session', 'produced', 'case://3', 'case://5')`,
		`INSERT INTO populations (workflow, name) VALUES ('wf', 'pop')`,
		`INSERT INTO population_members (workflow, name, resource_id, generation) VALUES ('wf', 'pop', 'case://2', 1)`,
		`INSERT INTO population_member_blockers (workflow, name, resource_id, position, reason) VALUES ('wf', 'pop', 'case://2', 0, 'busy')`,
		`INSERT INTO subscription_retries (session_id, action, resource_id) VALUES ('s-live', 'subscribe', 'case://4')`,
	}
	for _, stmt := range seed {
		if _, err := db.write.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	byLocator := map[string]string{}
	rows, err := db.write.QueryContext(ctx, `SELECT id, type, name, locator FROM resources`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, typ, name, locator string
		if err := rows.Scan(&id, &typ, &name, &locator); err != nil {
			t.Fatal(err)
		}
		if typ != "" || name != id {
			t.Errorf("resource %s = (type %q, name %q), want no type and its id as its name", locator, typ, name)
		}
		if _, dup := byLocator[locator]; dup {
			t.Errorf("identifier %q recorded twice", locator)
		}
		byLocator[locator] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(byLocator) != 5 {
		t.Fatalf("resources = %v, want one row for each of case://1..5", byLocator)
	}

	references := []struct {
		query   string
		locator string
	}{
		{`SELECT resource_id FROM sessions WHERE id = 's-live'`, "case://1"},
		{`SELECT resource_id FROM sessions WHERE id = 's-gone'`, "case://1"},
		{`SELECT resource_id FROM task_instances WHERE id = 'ti-1'`, "case://2"},
		{`SELECT resource_id FROM node_executions WHERE id = 'ne-1'`, "case://3"},
		{`SELECT subscription_id FROM node_executions WHERE id = 'ne-1'`, "case://5"},
		{`SELECT resource_id FROM population_members WHERE workflow = 'wf'`, "case://2"},
		{`SELECT resource_id FROM population_member_blockers WHERE workflow = 'wf'`, "case://2"},
		{`SELECT resource_id FROM subscription_retries WHERE session_id = 's-live'`, "case://4"},
	}
	for _, ref := range references {
		var got string
		if err := db.write.QueryRowContext(ctx, ref.query).Scan(&got); err != nil {
			t.Fatalf("%s: %v", ref.query, err)
		}
		if got != byLocator[ref.locator] {
			t.Errorf("%s = %q, want the row recorded for %s (%q)", ref.query, got, ref.locator, byLocator[ref.locator])
		}
	}
	var bare sql.NullString
	if err := db.write.QueryRowContext(ctx, `SELECT resource_id FROM sessions WHERE id = 's-bare'`).Scan(&bare); err != nil {
		t.Fatal(err)
	}
	if bare.Valid {
		t.Errorf("a session with no identifier references %q, want NULL", bare.String)
	}
	var tag string
	if err := db.write.QueryRowContext(ctx, `SELECT tag FROM sessions WHERE id = 's-live'`).Scan(&tag); err != nil || tag != "review" {
		t.Errorf("tag = %q, %v; want it kept", tag, err)
	}
	violations, err := db.write.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer violations.Close()
	if violations.Next() {
		t.Error("migrated database has a foreign key violation")
	}
}

const preSessionProjectRootVersion = 20261107200000

func TestMigrate_SessionProjectRootReplacesTheInvocationDirWithTheGlobalOnlyContext(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := formatTime(time.Now().UTC())
	provider, err := goose.NewProvider(goose.DialectSQLite3, db.write, db.migrations)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := provider.UpTo(ctx, preSessionProjectRootVersion); err != nil {
		t.Fatalf("UpTo(%d): %v", preSessionProjectRootVersion, err)
	}
	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO sessions (id, name, status, workflow, invocation_dir, created_at, updated_at) VALUES ('s1', 'review-1', 'up', 'review', '/projects/widgets/src', ?, ?)`,
		now, now,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("UpByOne: %v", err)
	}
	if sessionsHasColumn(t, db, "invocation_dir") {
		t.Error("invocation_dir survived the migration")
	}
	var root string
	if err := db.write.QueryRow(`SELECT project_root FROM sessions WHERE id = 's1'`).Scan(&root); err != nil {
		t.Fatalf("read project_root: %v", err)
	}
	if root != "" {
		t.Errorf("project_root = %q, want the global-only context: an invocation directory does not say which root it selected", root)
	}
}

const prePopulationProjectRootVersion = 20261107210000

func TestMigrate_PopulationIdentityGainsTheProjectRootWithoutLosingRows(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := formatTime(time.Now().UTC())
	provider, err := goose.NewProvider(goose.DialectSQLite3, db.write, db.migrations)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := provider.UpTo(ctx, prePopulationProjectRootVersion); err != nil {
		t.Fatalf("UpTo(%d): %v", prePopulationProjectRootVersion, err)
	}
	for _, stmt := range []string{
		`INSERT INTO resources (id, type, name, locator, created_at) VALUES ('res-1', 'issue', 'issue-1', 'https://forge.example/issues/1', '` + now + `')`,
		`INSERT INTO populations (workflow, name) VALUES ('review', 'standing')`,
		`INSERT INTO population_members (workflow, name, resource_id, session_name) VALUES ('review', 'standing', 'res-1', 'issue-1')`,
		`INSERT INTO population_member_blockers (workflow, name, resource_id, position, reason) VALUES ('review', 'standing', 'res-1', 0, 'waiting')`,
		`INSERT INTO sessions (id, name, status, workflow, population_workflow, population_name, created_at, updated_at) VALUES ('s1', 'issue-1', 'up', 'review', 'review', 'standing', '` + now + `', '` + now + `')`,
		`INSERT INTO node_instances (session_id, node_id) VALUES ('s1', 'pane')`,
	} {
		if _, err := db.write.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("UpByOne: %v", err)
	}

	for table, query := range map[string]string{
		"populations":                `SELECT count(*) FROM populations WHERE project_root = '' AND workflow = 'review' AND name = 'standing'`,
		"population_members":         `SELECT count(*) FROM population_members WHERE project_root = '' AND session_name = 'issue-1'`,
		"population_member_blockers": `SELECT count(*) FROM population_member_blockers WHERE project_root = '' AND reason = 'waiting'`,
		"sessions":                   `SELECT count(*) FROM sessions WHERE id = 's1' AND project_root = '' AND population_name = 'standing'`,
		"node_instances":             `SELECT count(*) FROM node_instances WHERE session_id = 's1'`,
	} {
		var n int
		if err := db.write.QueryRow(query).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s rows = %d (%v), want the row carried over in the global-only context", table, n, err)
		}
	}
	if _, err := db.write.ExecContext(ctx, `INSERT INTO populations (project_root, workflow, name) VALUES ('/projects/widgets', 'review', 'standing')`); err != nil {
		t.Errorf("a same-named population in another context collided: %v", err)
	}
	if _, err := db.write.ExecContext(ctx, `UPDATE sessions SET project_root = '/projects/gadgets' WHERE id = 's1'`); err == nil {
		t.Error("a session named a population of a context that declares none")
	}
}

func seedWorkflowNodeAt(t *testing.T, version int64, sessionStatus, workflowNodeStatus string) *DB {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	now := formatTime(time.Now().UTC())
	provider, err := goose.NewProvider(goose.DialectSQLite3, db.write, db.migrations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, version); err != nil {
		t.Fatalf("UpTo(%d): %v", version, err)
	}
	var destroyedAt sql.NullString
	if sessionStatus == "destroyed" {
		destroyedAt = sql.NullString{String: now, Valid: true}
	}
	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO sessions (id, name, status, destroyed_at, workflow, workspace_dir, created_at, updated_at) VALUES ('s1', 's1', ?, ?, 'wf', '/tmp/old-workspace', ?, ?)`,
		sessionStatus, destroyedAt, now, now,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if version < nodeExecutionsVersion {
		if _, err := db.write.ExecContext(ctx,
			`INSERT INTO node_instances (session_id, node_id, scope, status, sequence) VALUES ('s1', '@workflow', 'session', ?, 1)`,
			workflowNodeStatus,
		); err != nil {
			t.Fatalf("seed node_instances: %v", err)
		}
		return db
	}
	if _, err := db.write.ExecContext(ctx, `INSERT INTO node_instances (session_id, node_id) VALUES ('s1', '@workflow')`); err != nil {
		t.Fatalf("seed node_instances: %v", err)
	}
	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO node_executions (id, session_id, node_id, sequence, scope, status) VALUES ('exec-workflow', 's1', '@workflow', 1, 'session', ?)`,
		workflowNodeStatus,
	); err != nil {
		t.Fatalf("seed node_executions: %v", err)
	}
	return db
}

func TestMigrate_RefusesBeforeApplyingAnyMigrationWhileALiveSessionHoldsAnUnreleasedWorkflowNode(t *testing.T) {
	for _, version := range []int64{firstReleasedVersion, 20261104014706} {
		for _, workflowNodeStatus := range []string{"produced", "failed"} {
			t.Run(fmt.Sprintf("%d/%s", version, workflowNodeStatus), func(t *testing.T) {
				db := seedWorkflowNodeAt(t, version, "up", workflowNodeStatus)
				ctx := context.Background()

				err := db.Migrate(ctx)
				if err == nil {
					t.Fatal("Migrate succeeded, want a refusal while a live session holds an unreleased @workflow node")
				}
				if !strings.Contains(err.Error(), workdirDirectoryRefusal) {
					t.Errorf("error = %q, want the workdir-directory refusal %q", err, workdirDirectoryRefusal)
				}
				if current, err := db.Version(ctx); err != nil || current != version {
					t.Errorf("version after the refusal = (%d, %v), want %d unchanged", current, err, version)
				}
			})
		}
	}
}

func TestMigrate_UpgradesPastAWorkflowNodeThatNoLiveSessionHolds(t *testing.T) {
	tests := []struct {
		name               string
		sessionStatus      string
		workflowNodeStatus string
	}{
		{"released workflow node of a live session", "up", "cleaned"},
		{"unreleased workflow node of a destroyed session", "destroyed", "produced"},
	}
	for _, version := range []int64{firstReleasedVersion, 20261104014706} {
		for _, tc := range tests {
			t.Run(fmt.Sprintf("%d/%s", version, tc.name), func(t *testing.T) {
				db := seedWorkflowNodeAt(t, version, tc.sessionStatus, tc.workflowNodeStatus)
				ctx := context.Background()

				if err := db.Migrate(ctx); err != nil {
					t.Fatalf("Migrate: %v", err)
				}
				current, target, err := db.version(ctx)
				if err != nil || current != target {
					t.Errorf("version after Migrate = (%d, %v), want %d", current, err, target)
				}
			})
		}
	}
}
