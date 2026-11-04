//go:build integration

package task

import (
	"path/filepath"
	"strings"
	"testing"
)

// A session keeps the declarations and the recorded outputs it was created
// with, while the plugin's executables are whatever is installed when a
// lifecycle action runs. Updating a plugin therefore must not break a session
// that predates it, so each plugin that ships a released-version fixture under
// testdata/compat/<tag>/ has that release's task declarations and a scenario
// file built from that release's recorded outputs run against the plugin as it
// is now. The scenarios run under both the released declarations (old calls
// reaching current executables) and the current ones (a current probe reading
// a record written before it existed).
//
// A scenario that must pass is what is under contract here, not the calls it
// produced, so nothing is recorded: any action that declines fails the test.
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

// declinedSections names each recorded action whose outcome is a refusal,
// with the refusal, so a failure points at the effect and action that broke.
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
