package instructionorphans

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckFlagsAnUnreferencedSidecar(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "tasks", "work.toml"), `
[work]
kind = "task"
instructions = [{ file = "work.md" }]
`)
	write(t, filepath.Join(root, "tasks", "work.md"), "Resolve the issue.\n")
	write(t, filepath.Join(root, "tasks", "orphan.md"), "Nothing points at this file.\n")

	orphans, err := Check([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || !strings.Contains(orphans[0], "orphan.md") {
		t.Fatalf("orphans = %v, want exactly one naming orphan.md", orphans)
	}
}

// The canonical `[[<id>.instructions]]` array-of-tables spelling decodes to
// a different Go shape ([]map[string]any) than the inline
// `instructions = [{ ... }]` form ([]any) that the other tests in this file
// use — BurntSushi/toml picks the shape based on the TOML syntax, not on
// content. Both must count as a reference.
func TestCheckRecognizesTheArrayOfTablesForm(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "tasks", "work.toml"), `
[work]
kind = "task"

[[work.instructions]]
file = "work.md"

[[work.instructions]]
text = "An inline segment beside the sidecar."
`)
	write(t, filepath.Join(root, "tasks", "work.md"), "Resolve the issue.\n")

	orphans, err := Check([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %v, want none: the array-of-tables form must be recognized", orphans)
	}
}

// A commented-out `file = "..."` line is not TOML at all — it is not a
// reference, and this test would fail if the checker still scanned text
// rather than decoding.
func TestCheckIgnoresACommentedOutReference(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "tasks", "unrelated.toml"), `
[unrelated]
kind = "effect"
# instructions = [{ file = "orphan.md" }]
`)
	write(t, filepath.Join(root, "tasks", "orphan.md"), "Nothing points at this file.\n")

	orphans, err := Check([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || !strings.Contains(orphans[0], "orphan.md") {
		t.Fatalf("orphans = %v, want the commented-out reference ignored", orphans)
	}
}

// A commented-out `file = "..."` sitting on the same line as, or right
// beside, a real key is still not a reference once the line is decoded —
// this is the case an inline (not full-line) comment tests, distinct from
// TestCheckIgnoresACommentedOutReference's full-line comment.
func TestCheckIgnoresAnInlineCommentedReference(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "tasks", "unrelated.toml"), `
[unrelated]
kind = "effect" # file = "orphan.md"
`)
	write(t, filepath.Join(root, "tasks", "orphan.md"), "Nothing points at this file.\n")

	orphans, err := Check([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || !strings.Contains(orphans[0], "orphan.md") {
		t.Fatalf("orphans = %v, want the inline-commented reference ignored", orphans)
	}
}

// A quoted key spelling is the same key once TOML decodes it: `"file"` and
// `file` name the same field, so this must count as a reference exactly like
// the bare-key spelling does.
func TestCheckRecognizesAQuotedKeySpelling(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "tasks", "work.toml"), `
[work]
kind = "task"
instructions = [{ "file" = "work.md" }]
`)
	write(t, filepath.Join(root, "tasks", "work.md"), "Resolve the issue.\n")

	orphans, err := Check([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %v, want none: a quoted key is the same key", orphans)
	}
}

// A single-quoted (TOML literal) string value must count the same as a
// double-quoted one.
func TestCheckRecognizesASingleQuotedValue(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "tasks", "work.toml"), `
[work]
kind = "task"
instructions = [{ file = 'work.md' }]
`)
	write(t, filepath.Join(root, "tasks", "work.md"), "Resolve the issue.\n")

	orphans, err := Check([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %v, want none: a single-quoted value is still a reference", orphans)
	}
}

func TestCheckExcludesReadme(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "README.md"), "Not a sidecar.\n")

	orphans, err := Check([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %v, want README.md excluded", orphans)
	}
}

func TestCheckSkipsAMissingRoot(t *testing.T) {
	orphans, err := Check([]string{filepath.Join(t.TempDir(), "does-not-exist")})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %v, want none for a missing root", orphans)
	}
}
