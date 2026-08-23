package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

func TestSubscribeIfWired_EmptyResourceIsANoOp(t *testing.T) {
	cfg := &config.Config{BaseDir: t.TempDir()}
	wired, err := subscribeIfWired(cfg, "s", "")
	if err != nil || wired {
		t.Fatalf("wired=%v err=%v, want false, nil", wired, err)
	}
}

func TestSubscribeIfWired_NoProviderMatchIsSilentlySkipped(t *testing.T) {
	cfg := &config.Config{BaseDir: t.TempDir()}
	wired, err := subscribeIfWired(cfg, "s", "opaque-resource-no-provider-recognizes")
	if err != nil {
		t.Fatalf("subscribeIfWired: %v", err)
	}
	if wired {
		t.Error("a resource no workspace provider matches must not be reported as wired")
	}
}

func TestSubscribeIfWired_ProviderWithoutSubscribeHookIsSilentlySkipped(t *testing.T) {
	baseDir := t.TempDir()
	writeProviderDoc(t, baseDir, "github", ghMatch, "")
	cfg := &config.Config{BaseDir: baseDir}

	wired, err := subscribeIfWired(cfg, "s", "https://github.com/org/repo/pull/1")
	if err != nil {
		t.Fatalf("subscribeIfWired: %v", err)
	}
	if wired {
		t.Error("a provider without a subscribe hook must not be reported as wired")
	}
}

func TestSubscribeIfWired_RunsSubscribeHookAndReportsWired(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec")
	cfg := writeSubscribeProvider(t, "github", ghMatch, rec)

	wired, err := subscribeIfWired(cfg, "org/repo-7", "https://github.com/org/repo/pull/7")
	if err != nil {
		t.Fatalf("subscribeIfWired: %v", err)
	}
	if !wired {
		t.Error("a matched provider with a subscribe hook must be reported as wired")
	}
	got, readErr := os.ReadFile(rec)
	if readErr != nil {
		t.Fatalf("read record: %v", readErr)
	}
	want := "org/repo-7\nhttps://github.com/org/repo/pull/7\n"
	if string(got) != want {
		t.Errorf("hook recorded %q, want %q", got, want)
	}
}

func TestSubscribeIfWired_IdempotentOnRepeatedCall(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec")
	cfg := writeSubscribeProvider(t, "github", ghMatch, rec)

	for i := 0; i < 2; i++ {
		if _, err := subscribeIfWired(cfg, "org/repo-7", "https://github.com/org/repo/pull/7"); err != nil {
			t.Fatalf("subscribeIfWired[%d]: %v", i, err)
		}
	}
}

func TestSubscribeIfWired_AmbiguousMatchIsAnError(t *testing.T) {
	baseDir := t.TempDir()
	writeProviderDoc(t, baseDir, "a", ghMatch, "")
	writeProviderDoc(t, baseDir, "b", ghMatch, "")
	cfg := &config.Config{BaseDir: baseDir}

	_, err := subscribeIfWired(cfg, "s", "https://github.com/org/repo/pull/1")
	assertErrCode(t, err, ErrInvalidInput)
}

func TestSubscribeIfWired_HookFailureSurfacesStderr(t *testing.T) {
	baseDir := t.TempDir()
	body := `
[github]
kind  = "workspace_provider"
match = '` + ghMatch + `'
name  = { expr = "match.owner + '/' + match.repo + '-' + match.number" }

[github.setup]
type    = "exec"
command = "printf"
args    = ['{"workdir":"/tmp/x"}']

[github.subscribe]
type    = "exec"
command = "sh"
args    = ["-c", "echo boom >&2; exit 3"]
`
	writeFileService(t, filepath.Join(baseDir, "workspaces", "github.toml"), body)
	cfg := &config.Config{BaseDir: baseDir}

	_, err := subscribeIfWired(cfg, "s", "https://github.com/org/repo/pull/1")
	assertErrCode(t, err, ErrExecutionFailed)
}

func TestUnsubscribeIfWired_EmptyResourceIsANoOp(t *testing.T) {
	cfg := &config.Config{BaseDir: t.TempDir()}
	unsubscribed, err := unsubscribeIfWired(cfg, "s", "")
	if err != nil || unsubscribed {
		t.Fatalf("unsubscribed=%v err=%v, want false, nil", unsubscribed, err)
	}
}

func TestUnsubscribeIfWired_NoProviderMatchIsSilentlySkipped(t *testing.T) {
	cfg := &config.Config{BaseDir: t.TempDir()}
	unsubscribed, err := unsubscribeIfWired(cfg, "s", "opaque-resource-no-provider-recognizes")
	if err != nil || unsubscribed {
		t.Fatalf("unsubscribed=%v err=%v, want false, nil", unsubscribed, err)
	}
}

func TestUnsubscribeIfWired_ProviderWithoutUnsubscribeHookIsSilentlySkipped(t *testing.T) {
	baseDir := t.TempDir()
	writeProviderDoc(t, baseDir, "github", ghMatch, "")
	cfg := &config.Config{BaseDir: baseDir}

	unsubscribed, err := unsubscribeIfWired(cfg, "s", "https://github.com/org/repo/pull/1")
	if err != nil || unsubscribed {
		t.Fatalf("unsubscribed=%v err=%v, want false, nil", unsubscribed, err)
	}
}

func TestUnsubscribeIfWired_RunsUnsubscribeHook(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec")
	cfg := writeSubscribeUnsubscribeProvider(t, "github", ghMatch, "", rec)

	unsubscribed, err := unsubscribeIfWired(cfg, "org/repo-7", "https://github.com/org/repo/pull/7")
	if err != nil {
		t.Fatalf("unsubscribeIfWired: %v", err)
	}
	if !unsubscribed {
		t.Error("a matched provider with an unsubscribe hook must be reported as unsubscribed")
	}
	got, readErr := os.ReadFile(rec)
	if readErr != nil {
		t.Fatalf("read record: %v", readErr)
	}
	want := "org/repo-7\nhttps://github.com/org/repo/pull/7\n"
	if string(got) != want {
		t.Errorf("hook recorded %q, want %q", got, want)
	}
}

func TestUnsubscribeIfWired_AmbiguousMatchIsSilentlySkipped(t *testing.T) {
	baseDir := t.TempDir()
	writeProviderDoc(t, baseDir, "a", ghMatch, "")
	writeProviderDoc(t, baseDir, "b", ghMatch, "")
	cfg := &config.Config{BaseDir: baseDir}

	unsubscribed, err := unsubscribeIfWired(cfg, "s", "https://github.com/org/repo/pull/1")
	if err != nil || unsubscribed {
		t.Fatalf("unsubscribed=%v err=%v, want false, nil", unsubscribed, err)
	}
}

func TestUnsubscribeIfWired_HookFailureSurfacesStderr(t *testing.T) {
	baseDir := t.TempDir()
	body := `
[github]
kind  = "workspace_provider"
match = '` + ghMatch + `'
name  = { expr = "match.owner + '/' + match.repo + '-' + match.number" }

[github.setup]
type    = "exec"
command = "printf"
args    = ['{"workdir":"/tmp/x"}']

[github.unsubscribe]
type    = "exec"
command = "sh"
args    = ["-c", "echo boom >&2; exit 3"]
`
	writeFileService(t, filepath.Join(baseDir, "workspaces", "github.toml"), body)
	cfg := &config.Config{BaseDir: baseDir}

	unsubscribed, err := unsubscribeIfWired(cfg, "s", "https://github.com/org/repo/pull/1")
	if unsubscribed {
		t.Error("a failed hook must not be reported as unsubscribed")
	}
	assertErrCode(t, err, ErrExecutionFailed)
}

// writeProviderDoc writes a minimal workspace provider with a resolver and a
// no-op setup, plus the given extra table text (e.g. a subscribe hook),
// verbatim.
func writeProviderDoc(t *testing.T, baseDir, id, match, extra string) {
	t.Helper()
	body := `
[` + id + `]
kind  = "workspace_provider"
match = '` + match + `'
name  = { expr = "match.owner + '/' + match.repo + '-' + match.number" }

[` + id + `.setup]
type    = "exec"
command = "printf"
args    = ['{"workdir":"/tmp/x"}']
` + extra
	writeFileService(t, filepath.Join(baseDir, "workspaces", id+".toml"), body)
}

// writeSubscribeUnsubscribeProvider drops a provider with a resolver plus
// subscribe and unsubscribe hooks; subRecordPath/unsubRecordPath ("" to skip)
// record the rendered SessionName/ResourceID each hook ran with.
func writeSubscribeUnsubscribeProvider(t *testing.T, id, match, subRecordPath, unsubRecordPath string) *config.Config {
	t.Helper()
	baseDir := t.TempDir()
	var extra string
	if subRecordPath != "" {
		extra += `
[` + id + `.subscribe]
type    = "exec"
command = "sh"
args    = ["-c", 'printf "%s\n%s\n" "$1" "$2" > "$3"', "provider",
  { from = "session.name" }, { from = "resource.id" }, "` + subRecordPath + `"]
`
	}
	if unsubRecordPath != "" {
		extra += `
[` + id + `.unsubscribe]
type    = "exec"
command = "sh"
args    = ["-c", 'printf "%s\n%s\n" "$1" "$2" > "$3"', "provider",
  { from = "session.name" }, { from = "resource.id" }, "` + unsubRecordPath + `"]
`
	}
	writeProviderDoc(t, baseDir, id, match, extra)
	return &config.Config{BaseDir: baseDir}
}

func writeFileService(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
