//go:build integration

package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/state"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// TestIntegration_TickSpawnsReviewer drives the full fire path: a work
// session with a pending judge leaf and green checks, a task-embedded
// [[chains]] that wires `revision`, and plect tick (TickSession) spawning the
// reviewer workflow as a sibling under the work session's parent as part of
// the same tick that advances the Goal Loop. Re-ticking is idempotent.
func TestIntegration_TickSpawnsReviewer(t *testing.T) {
	workdirsRoot := setupE2ERepo(t)
	setupFakeScripts(t)
	store := state.NewStore(t.TempDir())

	cfg := writeIntegrationFixture(t, workdirsRoot, "default",
		[]taskFixture{
			{
				id:    "work",
				scope: "session",
				setup: `echo '{"checks_status":"SUCCESS","revision":"sha1"}'`,
				extra: `
[outputs_schema]
type = "object"
[outputs_schema.properties.revision]
type = "string"
[outputs_schema.properties.checks_status]
type = "string"

[done_when]
all = [
  { check = "checks_status", in = ["SUCCESS"] },
  { judge = "ac met", id = "ac-met" },
]

[[chains]]
id       = "review"
workflow = "default"
[chains.when]
all = [
  { judge_pending = "ac-met" },
  { check = "checks_status", in = ["SUCCESS"] },
]
[chains.inputs]
revision = "{{.Work.outputs.revision}}"
`,
			},
			{id: "tmux", scope: "run", setup: `echo '{"session_name":"t"}'`, cleanup: "true"},
		},
		[]nodeFixture{{id: "work"}, {id: "tmux"}},
	)

	url := "https://github.com/testowner/testrepo/issues/55"
	work := "testowner/testrepo-55+default"
	reviewer := "testowner/testrepo-55+review-work"

	if _, err := Up(cfg, store, UpParams{Identifier: url}); err != nil {
		t.Fatalf("Up(work): %v", err)
	}
	// Parent the work session so the sibling reviewer is tree-attached.
	seedSession(t, store, "testowner/testrepo-orch", "testowner/testrepo", 0, "", nil)
	setParent(t, store, work, "testowner/testrepo-orch")

	res, err := TickSession(cfg, store, TickParams{SessionName: work, SkipRefresh: true})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	sp, ok := findSpawn(res.Chains, "review")
	if !ok || !sp.Fired || !sp.Spawned {
		t.Fatalf("expected fired+spawned, got %+v", res.Chains)
	}
	if sp.TargetSession != reviewer {
		t.Fatalf("target = %q, want %q", sp.TargetSession, reviewer)
	}
	spawned := store.Get(reviewer)
	if spawned == nil {
		t.Fatalf("reviewer session %q not created", reviewer)
	}
	if spawned.ParentSession != "testowner/testrepo-orch" {
		t.Fatalf("reviewer parent = %q, want orchestrator", spawned.ParentSession)
	}
	if spawned.Workflow != "default" {
		t.Fatalf("reviewer workflow = %q, want default", spawned.Workflow)
	}
	if e := spawned.Tasks["work"]; e == nil || e.Status != contract.TaskStatusProduced {
		t.Fatalf("reviewer work task = %v, want produced", e)
	}
	// The wired `revision` binding is rendered from the work outputs and fed to
	// the spawned reviewer as a session input.
	if spawned.Inputs["revision"] != "sha1" {
		t.Fatalf("reviewer revision input = %v, want sha1", spawned.Inputs["revision"])
	}

	// Idempotent: a second tick finds the reviewer already active, no re-spawn.
	res2, err := TickSession(cfg, store, TickParams{SessionName: work, SkipRefresh: true})
	if err != nil {
		t.Fatalf("TickSession(2): %v", err)
	}
	sp2, _ := findSpawn(res2.Chains, "review")
	if !sp2.Fired || !sp2.AlreadyActive || sp2.Spawned {
		t.Fatalf("expected already-active without re-spawn, got %+v", sp2)
	}
}

// TestIntegration_LegacyChainsFileIsIgnored is the dual-read retirement
// counterpart to TestIntegration_TickSpawnsReviewer: a chains/*.toml
// declaration (not embedded in the task) is no longer read at all, so no
// reviewer is spawned even though the same rule would have fired before the
// legacy path was retired.
func TestIntegration_LegacyChainsFileIsIgnored(t *testing.T) {
	workdirsRoot := setupE2ERepo(t)
	setupFakeScripts(t)
	store := state.NewStore(t.TempDir())

	cfg := writeIntegrationFixture(t, workdirsRoot, "default",
		[]taskFixture{
			{
				id:    "work",
				scope: "session",
				setup: `echo '{"checks_status":"SUCCESS","revision":"sha1"}'`,
				extra: "[done_when]\nall = [\n  { check = \"resource.state.checks_status\", in = [\"SUCCESS\"] },\n  { judge = \"ac met\", id = \"ac-met\" },\n]\n",
			},
			{id: "tmux", scope: "run", setup: `echo '{"session_name":"t"}'`, cleanup: "true"},
		},
		[]nodeFixture{{id: "work"}, {id: "tmux"}},
	)
	dir := filepath.Join(cfg.BaseDir, "chains")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review.toml"), []byte(`
[[chains]]
id       = "review"
workflow = "default"
[chains.when]
all = [
  { judge_pending = "ac-met" },
  { check = "checks_status", in = ["SUCCESS"] },
]
[chains.inputs]
revision = "{{.Work.outputs.revision}}"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	url := "https://github.com/testowner/testrepo/issues/55"
	work := "testowner/testrepo-55+default"
	reviewer := "testowner/testrepo-55+review-work"

	if _, err := Up(cfg, store, UpParams{Identifier: url}); err != nil {
		t.Fatalf("Up(work): %v", err)
	}
	seedSession(t, store, "testowner/testrepo-orch", "testowner/testrepo", 0, "", nil)
	setParent(t, store, work, "testowner/testrepo-orch")

	res, err := TickSession(cfg, store, TickParams{SessionName: work, SkipRefresh: true})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	if len(res.Chains) != 0 {
		t.Fatalf("expected the legacy chains/*.toml declaration to be ignored, got %+v", res.Chains)
	}
	if store.Get(reviewer) != nil {
		t.Fatalf("reviewer session %q should not have been spawned from a legacy chains/*.toml declaration", reviewer)
	}
}
