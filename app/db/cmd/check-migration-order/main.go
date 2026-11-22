// Command check-migration-order fails a change whose new storage migration
// does not sort after the latest one on the base.
//
// Usage: check-migration-order <input-dir>. The directory holds the
// NUL-delimited repository-relative path lists base-migrations and
// added-migrations. scripts/check-migration-order.sh writes them from
// version control, so this command reads no repository itself. The base is the base's tip, not the point the change started
// from: a migration that landed on the base after that point is exactly the
// one the change's version must sort after.
package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pressly/goose/v3"
)

const (
	migrationsDir = "app/db/migrations"

	ruleOrder   = "migration-sorts-after-base"
	ruleVersion = "migration-has-version"
)

type input struct {
	BaseMigrations  []string
	AddedMigrations []string
}

type problem struct {
	File string
	Line int
	Rule string
	Fix  string
}

func (p problem) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", p.File, p.Line, p.Rule, p.Fix)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		if _, err := fmt.Fprintln(stderr, "usage: check-migration-order <input-dir>"); err != nil {
			return 2
		}
		return 2
	}
	in, err := readInput(args[0])
	if err != nil {
		if _, err := fmt.Fprintf(stderr, "check-migration-order: %v\n", err); err != nil {
			return 2
		}
		return 2
	}
	problems, err := check(in)
	if err != nil {
		if _, err := fmt.Fprintf(stderr, "check-migration-order: %v\n", err); err != nil {
			return 2
		}
		return 2
	}
	for _, p := range problems {
		if _, err := fmt.Fprintf(stderr, "check-migration-order: %s\n", p); err != nil {
			return 2
		}
	}
	if len(problems) > 0 {
		return 1
	}
	if _, err := fmt.Fprintf(stdout, "check-migration-order: %d new migration(s), all ordered\n", len(migrationFiles(in.AddedMigrations))); err != nil {
		return 2
	}
	return 0
}

func readInput(dir string) (input, error) {
	var in input
	lists := []struct {
		name string
		into *[]string
	}{
		{"base-migrations", &in.BaseMigrations},
		{"added-migrations", &in.AddedMigrations},
	}
	for _, l := range lists {
		content, err := os.ReadFile(filepath.Join(dir, l.name))
		if err != nil {
			return input{}, fmt.Errorf("read input list: %w", err)
		}
		for _, p := range strings.Split(string(content), "\x00") {
			if p != "" {
				*l.into = append(*l.into, p)
			}
		}
	}
	return in, nil
}

func check(in input) ([]problem, error) {
	latest, latestName, err := latestVersion(migrationFiles(in.BaseMigrations))
	if err != nil {
		return nil, err
	}
	var findings []problem
	for _, name := range migrationFiles(in.AddedMigrations) {
		v, err := goose.NumericComponent(name)
		switch {
		case err != nil:
			findings = append(findings, problem{name, 1, ruleVersion, fmt.Sprintf(
				"goose cannot read a version from this file name (%v); name it <version>_<description>.sql with a version later than every migration on the base", err)})
		case v <= latest:
			findings = append(findings, problem{name, 1, ruleOrder, fmt.Sprintf(
				"version %d does not sort after %s, the latest migration on the base. A store already at that version opens without ever applying this migration, and refuses to open once a later migration ships; bring the change up to date with the base, rename this file to a version later than %d, and regenerate migrations.sum with `go test ./db -update` in app",
				v, latestName, latest)})
		}
	}
	return findings, nil
}

func latestVersion(migrations []string) (int64, string, error) {
	var latest int64
	latestName := ""
	for _, name := range migrations {
		v, err := goose.NumericComponent(name)
		if err != nil {
			return 0, "", fmt.Errorf("base migration %s: %w", name, err)
		}
		if v > latest {
			latest, latestName = v, path.Base(name)
		}
	}
	return latest, latestName, nil
}

// migrationFiles keeps the files app/db embeds as the history goose
// applies, so a README or a nested file in the directory is not mistaken
// for a migration.
func migrationFiles(paths []string) []string {
	var out []string
	for _, p := range paths {
		if path.Dir(p) == migrationsDir && path.Ext(p) == ".sql" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
