package state

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLayerState_JSONUsesEffectID(t *testing.T) {
	raw, err := json.Marshal(LayerState{EffectID: "outer", Status: TaskStatusProduced})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"effect_id":"outer"`)) {
		t.Errorf("marshaled LayerState = %s, want an effect_id key", raw)
	}
	if bytes.Contains(raw, []byte(`"task_id"`)) {
		t.Errorf("marshaled LayerState = %s, want no task_id key", raw)
	}

	var decoded LayerState
	if err := json.Unmarshal([]byte(`{"effect_id":"inner","status":"produced"}`), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.EffectID != "inner" {
		t.Errorf("EffectID = %q, want %q", decoded.EffectID, "inner")
	}
}

// migrationFilter and migrationProcedure are copy-pasted from
// docs/migrations/task-layer-effect-id-migration.md rather than the doc
// loading this file (or vice versa) — there is no shared source of truth,
// so keeping them byte-for-byte identical is a manual obligation on whoever
// edits either one.
const migrationFilter = `(.sessions[]? | .tasks[]? | select(.layers != null) | .layers[]) |=
      (if has("task_id") then (. + {effect_id: .task_id} | del(.task_id)) else . end)`

const migrationProcedure = `
set -euo pipefail
jq '` + migrationFilter + `' \
  "$DATA_DIR/state.json" > "$DATA_DIR/state.json.new"

BAD=$(jq '[.sessions[]? | .tasks[]? | .layers[]? |
      select((.effect_id | type != "string") or (.effect_id == ""))] | length' \
  "$DATA_DIR/state.json.new")
if [ "$BAD" -ne 0 ]; then
  echo "migration verification failed: $BAD layer(s) have no valid effect_id after migration" >&2
  exit 1
fi

OLD_IDS=$(jq -S '[.sessions[]? | .tasks[]? | .layers[]? | (.effect_id // .task_id)] | sort' "$DATA_DIR/state.json")
NEW_IDS=$(jq -S '[.sessions[]? | .tasks[]? | .layers[]? | .effect_id] | sort' "$DATA_DIR/state.json.new")
if [ "$OLD_IDS" != "$NEW_IDS" ]; then
  echo "migration verification failed: layer identities changed — not replacing state.json" >&2
  exit 1
fi
mv "$DATA_DIR/state.json.new" "$DATA_DIR/state.json"
`

func skipIfNoJQ(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
}

func runMigrationFilter(t *testing.T, input string) string {
	t.Helper()
	skipIfNoJQ(t)
	cmd := exec.Command("jq", "-c", migrationFilter)
	cmd.Stdin = bytes.NewBufferString(input)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("jq: %v: %s", err, stderr.String())
	}
	return out.String()
}

// layerIDs returns a set rather than a slice: a migration must preserve
// which ids exist, not the order layers happen to appear in.
func layerIDs(t *testing.T, doc string) map[string]bool {
	t.Helper()
	var parsed struct {
		Sessions map[string]struct {
			Tasks map[string]struct {
				Layers []map[string]any `json:"layers"`
			} `json:"tasks"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	ids := map[string]bool{}
	for _, s := range parsed.Sessions {
		for _, task := range s.Tasks {
			for _, layer := range task.Layers {
				id, _ := layer["effect_id"].(string)
				if id == "" {
					id, _ = layer["task_id"].(string)
				}
				ids[id] = true
			}
		}
	}
	return ids
}

func TestMigrationFilter_FreshStateRenamesTaskIDToEffectID(t *testing.T) {
	const fresh = `{"sessions":{"s":{"tasks":{"outer":{"layers":[
		{"task_id":"outer","status":"produced"},
		{"task_id":"inner","status":"produced"}
	]}}}}}`
	got := runMigrationFilter(t, fresh)
	if bytes.Contains([]byte(got), []byte(`"task_id"`)) {
		t.Errorf("migrated = %s, want no task_id keys left", got)
	}
	before, after := layerIDs(t, fresh), layerIDs(t, got)
	for id := range before {
		if !after[id] {
			t.Errorf("layer %q lost after migration (got %v)", id, after)
		}
	}
}

// A layer with no "task_id" key (already migrated) makes `.task_id` read as
// JSON null in jq, not "absent" — an unconditional
// `. + {effect_id: .task_id}` overwrites a good effect_id with that null.
// This is the regression test for that.
func TestMigrationFilter_AlreadyMigratedStateIsUnchanged(t *testing.T) {
	const migrated = `{"sessions":{"s":{"tasks":{"outer":{"layers":[
		{"effect_id":"outer","status":"produced"}
	]}}}}}`
	got := runMigrationFilter(t, migrated)
	ids := layerIDs(t, got)
	if !ids["outer"] {
		t.Fatalf("migrated = %s, want effect_id %q preserved, not nulled", got, "outer")
	}
}

func TestMigrationFilter_PartiallyMigratedStateMigratesOnlyWhatNeedsIt(t *testing.T) {
	const mixed = `{"sessions":{"s":{"tasks":{"outer":{"layers":[
		{"effect_id":"outer","status":"produced"},
		{"task_id":"inner","status":"produced"}
	]}}}}}`
	got := runMigrationFilter(t, mixed)
	if bytes.Contains([]byte(got), []byte(`"task_id"`)) {
		t.Errorf("migrated = %s, want no task_id keys left", got)
	}
	ids := layerIDs(t, got)
	for _, want := range []string{"outer", "inner"} {
		if !ids[want] {
			t.Errorf("layer identities = %v, want %q preserved", ids, want)
		}
	}
}

// runMigrationProcedure returns the directory rather than the file content
// so a failure-path test can inspect exactly what, if anything, a rejected
// run left behind.
func runMigrationProcedure(t *testing.T, input string) (dataDir string, err error) {
	t.Helper()
	skipIfNoJQ(t)
	dataDir = t.TempDir()
	if writeErr := os.WriteFile(filepath.Join(dataDir, "state.json"), []byte(input), 0o644); writeErr != nil {
		t.Fatalf("write fixture: %v", writeErr)
	}
	cmd := exec.Command("bash", "-c", migrationProcedure)
	cmd.Env = append(os.Environ(), "DATA_DIR="+dataDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		t.Logf("migration procedure stderr: %s", stderr.String())
	}
	return dataDir, err
}

func TestMigrationProcedure_MigratesFreshStateAndReplacesTheFile(t *testing.T) {
	const fresh = `{"sessions":{"s":{"tasks":{"outer":{"layers":[
		{"task_id":"outer","status":"produced"}
	]}}}}}`
	dir, err := runMigrationProcedure(t, fresh)
	if err != nil {
		t.Fatalf("migration procedure: %v", err)
	}
	got, readErr := os.ReadFile(filepath.Join(dir, "state.json"))
	if readErr != nil {
		t.Fatalf("read state.json: %v", readErr)
	}
	if bytes.Contains(got, []byte(`"task_id"`)) {
		t.Errorf("state.json = %s, want task_id gone", got)
	}
	if !layerIDs(t, string(got))["outer"] {
		t.Errorf("state.json = %s, want effect_id %q", got, "outer")
	}
}

// A corrupted layer with a JSON-null task_id migrates to a JSON-null
// effect_id, and null compares equal to null on both sides of the
// identity-diff guard — that guard alone cannot catch this, which is why
// the not-a-real-string check exists as a separate, earlier gate.
func TestMigrationProcedure_RejectsANullIdentityWithoutTouchingTheFile(t *testing.T) {
	const corrupted = `{"sessions":{"s":{"tasks":{"outer":{"layers":[
		{"task_id":null,"status":"produced"}
	]}}}}}`
	dir, err := runMigrationProcedure(t, corrupted)
	if err == nil {
		t.Fatal("migration procedure: want a non-nil error for a null identity, got nil")
	}
	got, readErr := os.ReadFile(filepath.Join(dir, "state.json"))
	if readErr != nil {
		t.Fatalf("read state.json: %v", readErr)
	}
	if string(got) != corrupted {
		t.Errorf("state.json = %s, want it untouched after a rejected migration", got)
	}
}

// A layer manually half-edited to carry both keys (effect_id and task_id,
// with different values) passes the not-a-real-string check — task_id's
// value is a valid non-empty string — so this isolates the identity-diff
// guard: the filter's unconditional overwrite-on-has("task_id") silently
// prefers task_id's value, discarding the pre-existing effect_id, and only
// the diff against the pre-migration identity set catches that.
func TestMigrationProcedure_RejectsConflictingDualIdentityWithoutTouchingTheFile(t *testing.T) {
	const conflicting = `{"sessions":{"s":{"tasks":{"outer":{"layers":[
		{"effect_id":"outer","task_id":"renamed-outer","status":"produced"}
	]}}}}}`
	dir, err := runMigrationProcedure(t, conflicting)
	if err == nil {
		t.Fatal("migration procedure: want a non-nil error for conflicting dual identity, got nil")
	}
	got, readErr := os.ReadFile(filepath.Join(dir, "state.json"))
	if readErr != nil {
		t.Fatalf("read state.json: %v", readErr)
	}
	if string(got) != conflicting {
		t.Errorf("state.json = %s, want it untouched after a rejected migration", got)
	}
}

// set -euo pipefail should stop the procedure at the first jq failure
// rather than falling through to the guards with an empty or partial
// state.json.new; this is the test that proves it does.
func TestMigrationProcedure_AbortsOnMalformedInputWithoutTouchingTheFile(t *testing.T) {
	const broken = `{not valid json`
	dir, err := runMigrationProcedure(t, broken)
	if err == nil {
		t.Fatal("migration procedure: want a non-nil error for malformed input, got nil")
	}
	got, readErr := os.ReadFile(filepath.Join(dir, "state.json"))
	if readErr != nil {
		t.Fatalf("read state.json: %v", readErr)
	}
	if string(got) != broken {
		t.Errorf("state.json = %s, want it untouched after an aborted migration", got)
	}
}
