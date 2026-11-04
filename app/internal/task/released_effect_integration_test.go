//go:build integration

package task

import (
	"path/filepath"
	"strings"
	"testing"
)

// A session keeps the declarations and recorded outputs it was created with
// while the executables are whatever is installed when an action runs, so a
// plugin update must not break it. Each fixture under testdata/compat/<tag>/
// runs under the released and the current declarations, and any action that
// declines fails; no invocation record is kept because the pass is the contract.
func TestShippedEffects_ReleasedDeclarationsAndRecordsStillRun(t *testing.T) {
	for _, source := range repoPluginDirs(t) {
		fixtures, err := filepath.Glob(filepath.Join(source, "testdata", "compat", "*", "scenarios.toml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, fixture := range fixtures {
			dir := filepath.Dir(fixture)
			name := filepath.Base(source) + "/" + filepath.Base(dir)
			declarations := []struct{ name, dir string }{
				{"released-declarations", filepath.Join(dir, "config")},
				{"current-declarations", ""},
			}
			for _, d := range declarations {
				t.Run(name+"/"+d.name, func(t *testing.T) {
					scenarios := loadScenarioFile(t, fixture)
					if len(scenarios) == 0 {
						t.Fatalf("%s declares no scenarios", fixture)
					}
					produced := recordEffectsUnder(t, source, d.dir, scenarios)
					for _, declined := range declinedSections(produced) {
						t.Errorf("%s", declined)
					}
				})
			}
		}
	}
}

func declinedSections(produced string) []string {
	var out []string
	var header string
	for _, line := range strings.Split(produced, "\n") {
		switch {
		case strings.HasPrefix(line, "== "):
			header = strings.TrimPrefix(line, "== ")
		case strings.HasPrefix(line, "declined: "):
			out = append(out, header+": "+line)
		}
	}
	return out
}
