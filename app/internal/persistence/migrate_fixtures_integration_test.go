//go:build integration

package persistence

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

type rowCheck struct {
	query string
	want  []string
}

// survivingRows holds what every fixture's representative rows become
// at the current schema. Joins go through resources.locator, not
// resources.id, because the upgrade assigns resource ids at random.
var survivingRows = []rowCheck{
	{`SELECT name || '|' || status FROM sessions ORDER BY name`,
		[]string{"old-run|destroyed", "triage-7|up", "triage-7-review|down"}},
	{`SELECT c.name || '>' || p.name FROM sessions c JOIN sessions p ON p.id = c.parent_session_id`,
		[]string{"triage-7-review>triage-7"}},
	{`SELECT DISTINCT project_root FROM sessions`, []string{""}},
	{`SELECT locator FROM resources`, []string{"tracker:item/7"}},
	{`SELECT s.name FROM sessions s JOIN resources r ON r.id = s.resource_id WHERE r.locator = 'tracker:item/7'`,
		[]string{"triage-7"}},
	{`SELECT name || '|' || population_workflow || '|' || population_name FROM sessions WHERE population_name IS NOT NULL`,
		[]string{"triage-7|triage|inbox"}},
	{`SELECT project_root || '|' || workflow || '|' || name FROM populations`, []string{"|triage|inbox"}},
	{`SELECT m.project_root || '|' || m.session_name || '|' || m.generation || '|' || m.item_json FROM population_members m JOIN resources r ON r.id = m.resource_id WHERE r.locator = 'tracker:item/7'`,
		[]string{`|triage-7|1|{"title":"Seven"}`}},
	{`SELECT t.instance_name || '|' || t.status || '|' || t.inputs_json FROM task_instances t JOIN resources r ON r.id = t.resource_id WHERE r.locator = 'tracker:item/7'`,
		[]string{`initial|produced|{"item":"7"}`}},
	{`SELECT effect_id || '|' || outputs_json FROM task_instance_layers`, []string{`checkout|{"dir":"/work/7"}`}},
	{`SELECT last_action || '|' || heartbeat_ticks || '|' || last_reason FROM task_done_when_states`,
		[]string{"wait|2|checks pending"}},
	{`SELECT item FROM task_done_when_unsatisfied_items`, []string{"checks pass"}},
	{`SELECT leaf_id || '|' || action || '|' || relation FROM task_done_when_judges`, []string{"review|approve|child"}},
	{`SELECT node_id FROM node_instances ORDER BY node_id`, []string{"agent"}},
	{`SELECT ne.node_id || '|' || ne.status || '|' || ne.sequence FROM node_executions ne JOIN resources r ON r.id = ne.resource_id WHERE r.locator = 'tracker:item/7'`,
		[]string{"agent|produced|1"}},
	{`SELECT effect_id || '|' || outputs_json FROM node_execution_layers`, []string{`terminal|{"pane":"1"}`}},
	{`SELECT s.name || '|' || c.kind || '|' || c.next_sequence FROM event_cursors c JOIN sessions s ON s.id = c.session_id`,
		[]string{"triage-7|delivery|2"}},
	{`SELECT s.name || '|' || h.kind || '|' || h.consecutive_failures FROM session_channel_health h JOIN sessions s ON s.id = h.session_id`,
		[]string{"triage-7|delivery|1"}},
}

// stopEventAdded is what an upgrade across the down-and-sleep migration
// records for a session that was manually stopped before it.
var stopEventAdded = rowCheck{
	`SELECT s.name || '|' || e.sequence || '|' || e.type FROM events e JOIN sessions s ON s.id = e.session_id ORDER BY s.name, e.sequence`,
	[]string{"triage-7|1|plect.instruction", "triage-7-review|1|lifecycle.down", "triage-7-review|2|plect.session.stop"},
}

var eventsUnchanged = rowCheck{
	stopEventAdded.query,
	[]string{"triage-7|1|plect.instruction", "triage-7-review|1|lifecycle.down"},
}

var blockersKept = rowCheck{
	`SELECT b.reason FROM population_member_blockers b JOIN resources r ON r.id = b.resource_id WHERE r.locator = 'tracker:item/7'`,
	[]string{"waiting for review"},
}

// blockersLost is what the released admit-retry migration did to every
// store it upgraded: its PRAGMA foreign_keys = off is a no-op inside
// goose's transaction, so dropping the old population_members table
// cascaded to every blocker row. A released migration never changes, and
// the population evaluator recomputes a member's blockers on its next
// cycle.
var blockersLost = rowCheck{blockersKept.query, nil}

var sidecarRows = []rowCheck{
	{`SELECT instance || '|' || chain_id || '|' || fingerprint FROM chain_attempts`, []string{"initial|review|fp-1"}},
	{`SELECT sr.action FROM subscription_retries sr JOIN resources r ON r.id = sr.resource_id WHERE r.locator = 'tracker:item/7'`,
		[]string{"subscribe"}},
}

// revisionBackfillPending is the flag an upgrade across the
// workflow-revisions migration leaves for the runtime's own backfill of a
// live session's revision.
var revisionBackfillPending = rowCheck{`SELECT completed FROM workflow_revision_migration`, []string{"0"}}

var upgradeFixtures = map[string][]rowCheck{
	"20260907002408": append(slices.Clone(survivingRows), blockersLost, stopEventAdded, revisionBackfillPending),
	"20260907085032": append(slices.Clone(survivingRows), blockersLost, stopEventAdded, revisionBackfillPending),
	"20260907221219": append(slices.Clone(survivingRows), blockersLost, stopEventAdded, revisionBackfillPending),
	"20260908130000": append(slices.Concat(survivingRows, sidecarRows), blockersLost, stopEventAdded, revisionBackfillPending),
	"20261008155752": append(slices.Concat(survivingRows, sidecarRows), blockersKept, stopEventAdded, revisionBackfillPending),
	"20261104000000": append(slices.Concat(survivingRows, sidecarRows), blockersKept, eventsUnchanged, revisionBackfillPending),
	"20261104014706": append(slices.Concat(survivingRows, sidecarRows), blockersKept, eventsUnchanged,
		rowCheck{`SELECT completed FROM workflow_revision_migration`, []string{"1"}},
		rowCheck{`SELECT s.name || '|' || w.workflow || '|' || w.body FROM sessions s JOIN workflow_revisions w ON w.digest = s.workflow_revision`,
			[]string{`triage-7|triage|{"nodes":{}}`}},
	),
}

func TestMigrate_UpgradesEveryReleasedSchema(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "upgrade", "*.sql"))
	if err != nil {
		t.Fatalf("list fixtures: %v", err)
	}
	var fixtures []string
	for _, path := range paths {
		fixtures = append(fixtures, strings.TrimSuffix(filepath.Base(path), ".sql"))
	}
	var expected []string
	for fixture := range upgradeFixtures {
		expected = append(expected, fixture)
	}
	slices.Sort(expected)
	if !slices.Equal(fixtures, expected) {
		t.Fatalf("fixtures on disk %v do not match the fixtures with expected rows %v", fixtures, expected)
	}

	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			ctx := context.Background()
			db := openTestDB(t)
			loadUpgradeFixture(t, ctx, db, filepath.Join("testdata", "upgrade", fixture+".sql"))

			if err := db.Migrate(ctx); err != nil {
				t.Fatalf("Migrate from %s: %v", fixture, err)
			}
			current, target, err := db.version(ctx)
			if err != nil {
				t.Fatalf("version: %v", err)
			}
			if current != target {
				t.Fatalf("version after Migrate = %d, want %d", current, target)
			}
			for _, check := range upgradeFixtures[fixture] {
				if got := queryStrings(t, ctx, db, check.query); !slices.Equal(got, check.want) {
					t.Errorf("%s\n got  %q\n want %q", check.query, got, check.want)
				}
			}
		})
	}
}

// loadUpgradeFixture runs a `sqlite3 .dump` text, which turns foreign keys
// off for its own load, and turns them back on before Migrate runs: a
// migration that only works with enforcement off would otherwise pass
// here and fail on a real store.
func loadUpgradeFixture(t *testing.T, ctx context.Context, db *DB, path string) {
	t.Helper()
	fixture, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if _, err := db.write.ExecContext(ctx, string(fixture)); err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	if _, err := db.write.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("restore foreign keys: %v", err)
	}
	if violations := queryStrings(t, ctx, db, "SELECT \"table\" FROM pragma_foreign_key_check"); len(violations) > 0 {
		t.Fatalf("%s violates foreign keys in %v", path, violations)
	}
}

func queryStrings(t *testing.T, ctx context.Context, db *DB, query string) []string {
	t.Helper()
	rows, err := db.write.QueryContext(ctx, query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatalf("scan %s: %v", query, err)
		}
		got = append(got, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s: %v", query, err)
	}
	return got
}

// A fixture under testdata/upgrade/refused is a released schema whose rows
// the current migrations must refuse; the refusal is only safe to follow if
// that release's own binary can still open the store afterwards.
func TestMigrate_RefusedUpgradeLeavesTheReleasedSchemaOpenable(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "upgrade", "refused", "*.sql"))
	if err != nil {
		t.Fatalf("list fixtures: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no refused-upgrade fixtures found")
	}
	for _, fixturePath := range paths {
		fixture := strings.TrimSuffix(filepath.Base(fixturePath), ".sql")
		t.Run(fixture, func(t *testing.T) {
			released, err := strconv.ParseInt(fixture, 10, 64)
			if err != nil {
				t.Fatalf("fixture name %q is not a schema version: %v", fixture, err)
			}
			ctx := context.Background()
			path := PathIn(t.TempDir())
			db, err := Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			loadUpgradeFixture(t, ctx, db, fixturePath)
			before := queryStrings(t, ctx, db, workflowNodeExecutions)

			err = db.Migrate(ctx)
			if err == nil {
				t.Fatal("Migrate succeeded, want a refusal")
			}
			if !strings.Contains(err.Error(), workdirMigrationInstruction) {
				t.Errorf("error = %q, want it to name %s", err, workdirMigrationInstruction)
			}
			if current, err := db.Version(ctx); err != nil || current != released {
				t.Errorf("version after a refused Migrate = (%d, %v), want %d", current, err, released)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			previous, err := ensureCurrent(ctx, path, migrationsThrough(t, released), false, false)
			if err != nil {
				t.Fatalf("the release at schema %d cannot open the store after the refusal: %v", released, err)
			}
			defer previous.Close()
			if got := queryStrings(t, ctx, previous, workflowNodeExecutions); !slices.Equal(got, before) {
				t.Errorf("@workflow executions = %q after the refusal, want %q", got, before)
			}
		})
	}
}

const workflowNodeExecutions = `SELECT id || '|' || status FROM node_executions WHERE node_id = '@workflow' ORDER BY id`

// migrationsThrough is the migration source of a binary whose newest
// migration is version, standing in for that release's own binary.
func migrationsThrough(t *testing.T, version int64) fs.FS {
	t.Helper()
	source := migrationsSourceFS()
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	through := fstest.MapFS{}
	for _, entry := range entries {
		v, err := strconv.ParseInt(strings.SplitN(entry.Name(), "_", 2)[0], 10, 64)
		if err != nil || v > version {
			continue
		}
		body, err := fs.ReadFile(source, entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		through[entry.Name()] = &fstest.MapFile{Data: body}
	}
	return through
}
