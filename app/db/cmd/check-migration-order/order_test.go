package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

const (
	inboxMigration = "app/db/migrations/20261120000000_inbox_entries.sql"
	nextMigration  = "app/db/migrations/20261121000000_next.sql"
)

// adding returns a base whose latest migration is inboxMigration, with the
// given migrations added by the change.
func adding(migrations ...string) input {
	return input{
		BaseMigrations: []string{
			"app/db/migrations/20261116000000_last_kick_event_id.sql",
			inboxMigration,
		},
		AddedMigrations: migrations,
	}
}

func rulesOf(problems []problem) []string {
	var rules []string
	for _, p := range problems {
		rules = append(rules, p.Rule)
	}
	return rules
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name  string
		in    input
		rules []string
	}{
		{
			name: "a new migration after the base's latest passes without any procedure",
			in:   adding(nextMigration),
		},
		{
			name: "a change that adds no migration passes",
			in:   adding(),
		},
		{
			name:  "a new migration sorting before the base's latest fails",
			in:    adding("app/db/migrations/20261118000000_concurrent.sql"),
			rules: []string{ruleOrder},
		},
		{
			name:  "a new migration sharing the base's latest version fails",
			in:    adding("app/db/migrations/20261120000000_same_version.sql"),
			rules: []string{ruleOrder},
		},
		{
			name:  "every misordered new migration is reported",
			in:    adding("app/db/migrations/20261118000000_concurrent.sql", nextMigration, "app/db/migrations/20261119000000_also.sql"),
			rules: []string{ruleOrder, ruleOrder},
		},
		{
			name: "the first migration of an empty history is ordered",
			in: input{
				AddedMigrations: []string{"app/db/migrations/20260906051929_persistence_smoke.sql"},
			},
		},
		{
			name:  "an added file goose cannot read a version from fails",
			in:    adding("app/db/migrations/next.sql"),
			rules: []string{ruleVersion},
		},
		{
			name: "a README or a nested file is not a migration",
			in: adding(
				"app/db/migrations/README.md",
				"app/db/migrations/nested/20261101000000_nested.sql",
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems, err := check(tt.in)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if got := rulesOf(problems); !reflect.DeepEqual(got, tt.rules) {
				t.Fatalf("rules = %v, want %v\nproblems: %v", got, tt.rules, problems)
			}
		})
	}
}

func TestCheck_BaseMigrationWithoutVersionIsAnError(t *testing.T) {
	in := adding(nextMigration)
	in.BaseMigrations = []string{"app/db/migrations/broken.sql"}
	_, err := check(in)
	if err == nil || !strings.Contains(err.Error(), "broken.sql") {
		t.Fatalf("err = %v, want an error naming the unreadable base migration", err)
	}
}

func TestProblemString_NamesFileLineRuleAndTheBaseLatest(t *testing.T) {
	problems, err := check(adding("app/db/migrations/20261118000000_concurrent.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want 1", problems)
	}
	got := problems[0].String()
	for _, want := range []string{"app/db/migrations/20261118000000_concurrent.sql:1: ", ruleOrder, "20261120000000_inbox_entries.sql"} {
		if !strings.Contains(got, want) {
			t.Errorf("problem %q does not contain %q", got, want)
		}
	}
}

func TestReadInput(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("base-migrations", inboxMigration+"\x00")
	write("added-migrations", nextMigration+"\x00")

	got, err := readInput(dir)
	if err != nil {
		t.Fatalf("readInput: %v", err)
	}
	want := input{
		BaseMigrations:  []string{inboxMigration},
		AddedMigrations: []string{nextMigration},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readInput = %+v, want %+v", got, want)
	}

	if err := os.Remove(filepath.Join(dir, "added-migrations")); err != nil {
		t.Fatal(err)
	}
	if _, err := readInput(dir); err == nil || !strings.Contains(err.Error(), "added-migrations") {
		t.Fatalf("err = %v, want an error naming the missing list", err)
	}
}

func TestRun_ExitStatus(t *testing.T) {
	tests := []struct {
		name     string
		added    string
		args     func(dir string) []string
		code     int
		inStderr string
	}{
		{
			name:  "an ordered migration passes",
			added: nextMigration + "\x00",
			code:  0,
		},
		{
			name:     "a misordered migration fails naming the rule",
			added:    "app/db/migrations/20261118000000_concurrent.sql\x00",
			code:     1,
			inStderr: ruleOrder,
		},
		{
			name:     "a usage error exits 2",
			args:     func(string) []string { return nil },
			code:     2,
			inStderr: "usage",
		},
		{
			name:     "an unreadable input exits 2",
			args:     func(dir string) []string { return []string{filepath.Join(dir, "missing")} },
			code:     2,
			inStderr: "missing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"base-migrations":  inboxMigration + "\x00",
				"added-migrations": tt.added,
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{dir}
			if tt.args != nil {
				args = tt.args(dir)
			}
			var stdout, stderr strings.Builder
			code := run(args, &stdout, &stderr)
			if code != tt.code {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, tt.code, stderr.String())
			}
			if tt.inStderr != "" && !strings.Contains(stderr.String(), tt.inStderr) {
				t.Fatalf("stderr %q does not contain %q", stderr.String(), tt.inStderr)
			}
		})
	}
}

func TestRun_OutputFailureExitsWithError(t *testing.T) {
	tests := []struct {
		name       string
		base       string
		added      string
		args       func(string) []string
		failStdout bool
	}{
		{
			name: "usage",
			args: func(string) []string { return nil },
		},
		{
			name: "input error",
			args: func(dir string) []string { return []string{filepath.Join(dir, "missing")} },
		},
		{
			name: "base migration error",
			base: "app/db/migrations/broken.sql\x00",
		},
		{
			name:  "violation",
			base:  inboxMigration + "\x00",
			added: "app/db/migrations/20261118000000_concurrent.sql\x00",
		},
		{
			name:       "success",
			base:       inboxMigration + "\x00",
			added:      nextMigration + "\x00",
			failStdout: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range map[string]string{
				"base-migrations":  tt.base,
				"added-migrations": tt.added,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{dir}
			if tt.args != nil {
				args = tt.args(dir)
			}
			var stdout, stderr io.Writer = &strings.Builder{}, failingWriter{}
			if tt.failStdout {
				stdout, stderr = failingWriter{}, &strings.Builder{}
			}
			if code := run(args, stdout, stderr); code != 2 {
				t.Fatalf("exit = %d, want 2 for an output failure", code)
			}
		})
	}
}
