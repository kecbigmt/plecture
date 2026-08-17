package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

const oldStateJSON = `{
  "version": 5,
  "sessions": {
    "acme-issue-1": {
      "session_name": "acme-issue-1",
      "url": "https://github.com/acme/widgets/issues/1",
      "url_type": "issue",
      "owner_repo": "acme/widgets",
      "number": 1,
      "branch": "issue-1",
      "workdir_path": "/tmp/worktrees/acme-issue-1",
      "slack": {
        "thread_ts": "1700000000.000100",
        "channel_id": "C123"
      },
      "effects": {
        "setup": {
          "scope": "session",
          "status": "produced",
          "effect_id": "setup-task"
        }
      },
      "created_at": "2024-01-01T00:00:00Z",
      "updated_at": "2024-01-01T00:00:00Z"
    },
    "inline-legacy": {
      "session_name": "inline-legacy",
      "url": "https://github.com/acme/widgets/issues/2",
      "url_type": "issue",
      "owner_repo": "acme/widgets",
      "number": 2,
      "branch": "issue-2",
      "workdir_path": "/tmp/worktrees/inline-legacy",
      "tasks": {
        "build": {
          "scope": "session",
          "status": "produced"
        }
      },
      "created_at": "2024-01-01T00:00:00Z",
      "updated_at": "2024-01-01T00:00:00Z"
    }
  }
}`

const newStateJSON = `{
  "version": 7,
  "sessions": {
    "acme-issue-1": {
      "session_name": "acme-issue-1",
      "resource_id": "https://github.com/acme/widgets/issues/1",
      "alias": "https://github.com/acme/widgets/issues/1",
      "branch": "issue-1",
      "workspace_dir_path": "/tmp/worktrees/acme-issue-1",
      "conversation": {
        "source": "Slack",
        "metadata": {
          "thread_ts": "1700000000.000100",
          "channel_id": "C123"
        }
      },
      "tasks": {
        "setup": {
          "scope": "session",
          "status": "produced",
          "task_id": "setup-task"
        }
      },
      "created_at": "2024-01-01T00:00:00Z",
      "updated_at": "2024-01-01T00:00:00Z"
    },
    "inline-legacy": {
      "session_name": "inline-legacy",
      "resource_id": "https://github.com/acme/widgets/issues/2",
      "alias": "https://github.com/acme/widgets/issues/2",
      "branch": "issue-2",
      "workspace_dir_path": "/tmp/worktrees/inline-legacy",
      "tasks": {
        "build": {
          "scope": "session",
          "status": "produced",
          "task_id": "build"
        }
      },
      "created_at": "2024-01-01T00:00:00Z",
      "updated_at": "2024-01-01T00:00:00Z"
    }
  }
}`

const oldConfigTOML = `worktrees_root = "/home/user/worktrees"
repo_allowlist = ["acme/widgets", "acme/gadgets"]
detached = true
`

func writeFixtures(t *testing.T, dir string) (statePath, configPath string) {
	t.Helper()
	statePath = filepath.Join(dir, "state.json")
	configPath = filepath.Join(dir, "config.toml")
	if err := os.WriteFile(statePath, []byte(oldStateJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(oldConfigTOML), 0644); err != nil {
		t.Fatal(err)
	}
	return statePath, configPath
}

func decodeJSONMap(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	return m
}

func sequentialClock(start time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		i++
		return start.Add(time.Duration(i) * time.Second)
	}
}

func TestRun_MigratesOldFormAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	statePath, configPath := writeFixtures(t, dir)
	backupRoot := filepath.Join(dir, "migration-backups")
	clock := sequentialClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	report1, err := Run(Options{StatePath: statePath, ConfigPath: configPath, BackupDir: backupRoot, Now: clock})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !report1.Changed || !report1.StateChanged || !report1.ConfigChanged {
		t.Fatalf("expected first run to change both files, got %+v", report1)
	}
	if report1.BackupDir == "" {
		t.Fatal("expected first run to create a backup dir")
	}

	gotState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := decodeJSONMap(t, gotState), decodeJSONMap(t, []byte(newStateJSON)); !jsonEqual(got, want) {
		t.Fatalf("state.json mismatch\ngot:  %s\nwant: %s", gotState, newStateJSON)
	}

	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if containsKey(t, gotConfig, "repo_allowlist") {
		t.Fatal("expected repo_allowlist to be removed from config.toml")
	}
	if containsKey(t, gotConfig, "worktrees_root") || containsKey(t, gotConfig, "workdirs_root") || !containsKey(t, gotConfig, "workspace_dirs_root") {
		t.Fatalf("expected worktrees_root to be renamed to workspace_dirs_root, got:\n%s", gotConfig)
	}
	patterns := decodeStringSlice(t, gotConfig, "resource_allowlist")
	wantPatterns := map[string]bool{
		`^https://github\.com/acme/widgets(/|$)`: true,
		`^https://github\.com/acme/gadgets(/|$)`: true,
	}
	if len(patterns) != len(wantPatterns) {
		t.Fatalf("resource_allowlist = %v, want patterns for %v", patterns, wantPatterns)
	}
	for _, p := range patterns {
		if !wantPatterns[p] {
			t.Fatalf("unexpected resource_allowlist pattern %q", p)
		}
	}

	// Backed-up originals must be byte-identical to the pre-migration files.
	backedUpState, err := os.ReadFile(filepath.Join(report1.BackupDir, "state.json"))
	if err != nil {
		t.Fatalf("read backed-up state.json: %v", err)
	}
	if string(backedUpState) != oldStateJSON {
		t.Fatal("backed-up state.json is not byte-identical to the original")
	}
	backedUpConfig, err := os.ReadFile(filepath.Join(report1.BackupDir, "config.toml"))
	if err != nil {
		t.Fatalf("read backed-up config.toml: %v", err)
	}
	if string(backedUpConfig) != oldConfigTOML {
		t.Fatal("backed-up config.toml is not byte-identical to the original")
	}

	// Second run against the now-migrated data is a no-op.
	report2, err := Run(Options{StatePath: statePath, ConfigPath: configPath, BackupDir: backupRoot, Now: clock})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if report2.Changed {
		t.Fatalf("expected second run on migrated data to be a no-op, got %+v", report2)
	}
	if report2.BackupDir != "" {
		t.Fatal("expected no-op run to create no backup")
	}
}

func TestRun_TwoRunsAgainstOldFormProduceTwoBackups(t *testing.T) {
	dir := t.TempDir()
	statePath, configPath := writeFixtures(t, dir)
	backupRoot := filepath.Join(dir, "migration-backups")
	clock := sequentialClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	report1, err := Run(Options{StatePath: statePath, ConfigPath: configPath, BackupDir: backupRoot, Now: clock})
	if err != nil {
		t.Fatal(err)
	}

	// Reintroduce old-form data (simulating a second, independent old-form
	// data directory) to exercise a second real migration run.
	writeFixtures(t, dir)
	report2, err := Run(Options{StatePath: statePath, ConfigPath: configPath, BackupDir: backupRoot, Now: clock})
	if err != nil {
		t.Fatal(err)
	}

	if !report1.Changed || !report2.Changed {
		t.Fatalf("expected both runs to report changes, got %+v and %+v", report1, report2)
	}
	if report1.BackupDir == report2.BackupDir {
		t.Fatalf("expected distinct backup dirs, got the same dir %q twice", report1.BackupDir)
	}
	entries, err := os.ReadDir(backupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 backup subdirectories, got %d", len(entries))
	}
}

func TestRun_RollbackRestoresByteIdenticalOriginal(t *testing.T) {
	dir := t.TempDir()
	statePath, configPath := writeFixtures(t, dir)
	backupRoot := filepath.Join(dir, "migration-backups")

	report, err := Run(Options{StatePath: statePath, ConfigPath: configPath, BackupDir: backupRoot, Now: func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}})
	if err != nil {
		t.Fatal(err)
	}

	// Rollback procedure: copy the backed-up files back over the current
	// (migrated) files.
	for _, name := range []string{"state.json", "config.toml"} {
		data, err := os.ReadFile(filepath.Join(report.BackupDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	restoredState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restoredState) != oldStateJSON {
		t.Fatal("rollback did not restore state.json byte-identically")
	}
	restoredConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restoredConfig) != oldConfigTOML {
		t.Fatal("rollback did not restore config.toml byte-identically")
	}
}

func TestRun_CurrentFormDataIsNoOp(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(statePath, []byte(newStateJSON), 0644); err != nil {
		t.Fatal(err)
	}
	newConfig := `workspace_dirs_root = "/home/user/workspace_dirs"
resource_allowlist = ["^https://github\\.com/acme/widgets(/|$)"]
detached = true
`
	if err := os.WriteFile(configPath, []byte(newConfig), 0644); err != nil {
		t.Fatal(err)
	}

	report, err := Run(Options{StatePath: statePath, ConfigPath: configPath, BackupDir: filepath.Join(dir, "migration-backups")})
	if err != nil {
		t.Fatal(err)
	}
	if report.Changed {
		t.Fatalf("expected no-op on already-migrated data, got %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dir, "migration-backups")); !os.IsNotExist(err) {
		t.Fatal("expected no backup directory to be created for a no-op run")
	}
}

func TestRun_BumpsPreGuardStateVersion(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	configPath := filepath.Join(dir, "config.toml")
	preGuard := `{
  "version": 5,
  "sessions": {
    "acme-issue-1": {
      "session_name": "acme-issue-1",
      "resource_id": "https://example.test/acme/widgets/items/1",
      "alias": "https://example.test/acme/widgets/items/1",
      "workdir_path": "/tmp/workdirs/acme-issue-1"
    }
  }
}`
	if err := os.WriteFile(statePath, []byte(preGuard), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	report, err := Run(Options{StatePath: statePath, ConfigPath: configPath, BackupDir: filepath.Join(dir, "migration-backups")})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Changed || !report.StateChanged || report.ConfigChanged {
		t.Fatalf("expected only state.json to be rewritten, got %+v", report)
	}

	got := decodeJSONMap(t, mustReadFile(t, statePath))
	if got["version"] != float64(7) {
		t.Fatalf("version = %v, want 7", got["version"])
	}
	session := got["sessions"].(map[string]any)["acme-issue-1"].(map[string]any)
	if session["workspace_dir_path"] != "/tmp/workdirs/acme-issue-1" {
		t.Fatalf("workspace_dir_path = %v, want it renamed from workdir_path", session["workspace_dir_path"])
	}
}

// TestRun_WorkdirEraStateVersionAlsoMigrates pins that this tool accepts
// input already at the workdir-era schema version (the prior, now-retired
// worktree→workdir migration's own output), not just the oldest pre-guard
// version — so an operator who already ran that migration does not have to
// chain a second one.
func TestRun_WorkdirEraStateVersionAlsoMigrates(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	configPath := filepath.Join(dir, "config.toml")
	workdirEra := `{
  "version": 6,
  "sessions": {
    "acme-issue-1": {
      "session_name": "acme-issue-1",
      "resource_id": "https://example.test/acme/widgets/items/1",
      "alias": "https://example.test/acme/widgets/items/1",
      "workdir_path": "/tmp/workdirs/acme-issue-1",
      "tasks": {
        "@workflow": {
          "scope": "session",
          "status": "produced",
          "outputs": {"workdir": "/tmp/workdirs/acme-issue-1", "branch": "issue-1"}
        }
      }
    }
  }
}`
	if err := os.WriteFile(statePath, []byte(workdirEra), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`workdirs_root = "/home/user/workdirs"`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	report, err := Run(Options{StatePath: statePath, ConfigPath: configPath, BackupDir: filepath.Join(dir, "migration-backups")})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Changed || !report.StateChanged || !report.ConfigChanged {
		t.Fatalf("expected both files to be rewritten, got %+v", report)
	}

	got := decodeJSONMap(t, mustReadFile(t, statePath))
	if got["version"] != float64(7) {
		t.Fatalf("version = %v, want 7", got["version"])
	}
	session := got["sessions"].(map[string]any)["acme-issue-1"].(map[string]any)
	if session["workspace_dir_path"] != "/tmp/workdirs/acme-issue-1" {
		t.Fatalf("workspace_dir_path = %v, want it renamed from workdir_path", session["workspace_dir_path"])
	}
	if _, exists := session["workdir_path"]; exists {
		t.Fatal("legacy workdir_path key must be removed")
	}
	wfOutputs := session["tasks"].(map[string]any)["@workflow"].(map[string]any)["outputs"].(map[string]any)
	if wfOutputs["workspace_dir"] != "/tmp/workdirs/acme-issue-1" {
		t.Fatalf("@workflow outputs.workspace_dir = %v, want it renamed from outputs.workdir", wfOutputs["workspace_dir"])
	}
	if _, exists := wfOutputs["workdir"]; exists {
		t.Fatal("legacy @workflow outputs.workdir key must be removed")
	}

	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if containsKey(t, gotConfig, "workdirs_root") || !containsKey(t, gotConfig, "workspace_dirs_root") {
		t.Fatalf("expected workdirs_root to be renamed to workspace_dirs_root, got:\n%s", gotConfig)
	}
}

func TestRun_RepairsEmptyWorkspaceDirPathFromLegacyWorktreePath(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	configPath := filepath.Join(dir, "config.toml")
	input := `{
  "version": 5,
  "sessions": {
    "acme-issue-1": {
      "session_name": "acme-issue-1",
      "workdir_path": "",
      "worktree_path": "/tmp/worktrees/acme-issue-1"
    }
  }
}`
	if err := os.WriteFile(statePath, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	report, err := Run(Options{StatePath: statePath, ConfigPath: configPath, BackupDir: filepath.Join(dir, "migration-backups")})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Changed || !report.StateChanged {
		t.Fatalf("expected state.json to be rewritten, got %+v", report)
	}

	got := decodeJSONMap(t, mustReadFile(t, statePath))
	session := got["sessions"].(map[string]any)["acme-issue-1"].(map[string]any)
	if session["workspace_dir_path"] != "/tmp/worktrees/acme-issue-1" {
		t.Fatalf("workspace_dir_path = %v, want restored legacy path", session["workspace_dir_path"])
	}
	if _, exists := session["worktree_path"]; exists {
		t.Fatal("legacy worktree_path key must be removed")
	}
	if _, exists := session["workdir_path"]; exists {
		t.Fatal("legacy workdir_path key must be removed")
	}
	if got["version"] != float64(7) {
		t.Fatalf("version = %v, want 7", got["version"])
	}
}

func TestRun_MissingFilesIsNoOp(t *testing.T) {
	dir := t.TempDir()
	report, err := Run(Options{
		StatePath:  filepath.Join(dir, "state.json"),
		ConfigPath: filepath.Join(dir, "config.toml"),
		BackupDir:  filepath.Join(dir, "migration-backups"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Changed {
		t.Fatalf("expected no-op when neither file exists, got %+v", report)
	}
}

func jsonEqual(a, b map[string]any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	var na, nb any
	_ = json.Unmarshal(aj, &na)
	_ = json.Unmarshal(bj, &nb)
	return deepEqualJSON(na, nb)
}

func deepEqualJSON(a, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

func decodeTOML(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if _, err := toml.Decode(string(data), &doc); err != nil {
		t.Fatalf("decode toml: %v", err)
	}
	return doc
}

func containsKey(t *testing.T, tomlData []byte, key string) bool {
	t.Helper()
	doc := decodeTOML(t, tomlData)
	_, ok := doc[key]
	return ok
}

func decodeStringSlice(t *testing.T, tomlData []byte, key string) []string {
	t.Helper()
	doc := decodeTOML(t, tomlData)
	raw, _ := doc[key].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
