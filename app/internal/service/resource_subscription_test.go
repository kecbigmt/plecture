package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

func TestSubscribeUsesResourceWithoutWorkspaceProvider(t *testing.T) {
	base := t.TempDir()
	record := filepath.Join(base, "record")
	writeResourceDefFixture(t, base, "remote", `[remote]
kind = "resource"
name = "fixture"
match = '^remote://'
[remote.observe]
type = "exec"
command = "printf"
args = ['{}']
[remote.subscribe]
type = "exec"
command = "sh"
args = ["-c", "printf '%s|%s|%s' \"$1\" \"$2\" \"$3\" > \"$4\"", "hook", { from = "resource.id" }, { from = "session.name" }, { from = "session.branch", default = "" }, "`+record+`"]
`)
	cfg := &config.Config{BaseDir: base}
	if err := Subscribe(cfg, subscribeStore(t, "session"), SubscribeParams{ResourceID: "remote://one", SessionName: "session"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "remote://one|session|" {
		t.Fatalf("hook args = %q", got)
	}
}

func TestSubscribeRejectsAmbiguousResourceDeclarations(t *testing.T) {
	base := t.TempDir()
	for _, id := range []string{"one", "two"} {
		writeResourceDefFixture(t, base, id, "["+id+"]\nkind = \"resource\"\nname = \"fixture\"\nmatch = '^remote://'\n["+id+".observe]\ntype = \"exec\"\ncommand = \"printf\"\nargs = ['{}']\n["+id+".subscribe]\ntype = \"exec\"\ncommand = \"true\"\n")
	}
	err := Subscribe(&config.Config{BaseDir: base}, subscribeStore(t, "session"), SubscribeParams{ResourceID: "remote://one", SessionName: "session"})
	if err == nil || !strings.Contains(err.Error(), "multiple resource") {
		t.Fatalf("Subscribe error = %v", err)
	}
}

func TestSubscribePassesResourceInputs(t *testing.T) {
	base := t.TempDir()
	record := filepath.Join(base, "record")
	writeResourceDefFixture(t, base, "remote", `[remote]
kind = "resource"
name = "fixture"
match = '^remote://'
[remote.observe]
type = "exec"
command = "printf"
args = ['{}']
[remote.subscribe]
type = "exec"
command = "sh"
args = ["-c", "printf '%s' \"$1\" > \"$2\"", "hook", { from = "resource.inputs.token" }, "`+record+`"]
[remote.inputs_schema]
type = "object"
required = ["token"]
[remote.inputs_schema.properties]
token = { type = "string" }
`)
	cfg := &config.Config{BaseDir: base}
	store := subscribeStore(t, "session")
	if err := Subscribe(cfg, store, SubscribeParams{ResourceID: "remote://one", SessionName: "session", Inputs: map[string]any{"token": "secret"}}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(record)
	if err != nil || string(got) != "secret" {
		t.Fatalf("hook input = %q, %v", got, err)
	}
	if err := Subscribe(cfg, store, SubscribeParams{ResourceID: "remote://one", SessionName: "session"}); err == nil {
		t.Fatal("missing required resource input was accepted")
	}
}
