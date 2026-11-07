package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// gatedCaseResourceDoc is a `case` resource whose actions require a token
// and append what they saw to log; subscribe fails while failFlag exists.
func gatedCaseResourceDoc(log, failFlag string) string {
	return fmt.Sprintf(`[case]
kind  = "resource"
match = '^case://(?P<n>[0-9]+)$'
name  = { expr = "'case-' + match.n" }

[case.observe]
type   = "shell"
script = '''
printf 'observe %%s %%s\n' "$id" "$token" >> %[1]q
echo '{}'
'''

[case.observe.bind]
id    = { from = "resource.id" }
token = { from = "resource.inputs.token" }

[case.subscribe]
type   = "shell"
script = '''
if [ -e %[2]q ]; then exit 1; fi
printf 'subscribe %%s %%s\n' "$id" "$token" >> %[1]q
'''

[case.subscribe.bind]
id    = { from = "resource.id" }
token = { from = "resource.inputs.token" }

[case.unsubscribe]
type   = "shell"
script = '''
printf 'unsubscribe %%s %%s\n' "$id" "$token" >> %[1]q
'''

[case.unsubscribe.bind]
id    = { from = "resource.id" }
token = { from = "resource.inputs.token" }

[case.inputs_schema]
type                 = "object"
required             = ["token"]
additionalProperties = false

[case.inputs_schema.properties]
token = { type = "string" }
`, log, failFlag)
}

type gatedFixture struct {
	cfg      *config.Config
	log      string
	failFlag string
}

func newGatedFixture(t *testing.T) gatedFixture {
	t.Helper()
	dir := t.TempDir()
	f := gatedFixture{log: filepath.Join(dir, "actions.log"), failFlag: filepath.Join(dir, "fail")}
	f.cfg = writeWorkflowFixture(t, "flow",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	useResource(t, f.cfg, "flow", "case", gatedCaseResourceDoc(f.log, f.failFlag))
	return f
}

func (f gatedFixture) lines(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func (f gatedFixture) requireLine(t *testing.T, want string) {
	t.Helper()
	for _, line := range f.lines(t) {
		if line == want {
			return
		}
	}
	t.Errorf("action log = %q, want a line %q", f.lines(t), want)
}

func TestUp_RejectsEntryInputsTheResourceSchemaRefuses(t *testing.T) {
	for name, inputs := range map[string]map[string]any{
		"missing":    nil,
		"wrong":      {"token": 7},
		"undeclared": {"token": "t", "extra": "x"},
	} {
		t.Run(name, func(t *testing.T) {
			store := testStore(t)
			f := newGatedFixture(t)
			_, err := Up(f.cfg, store, UpParams{Identifier: identityCaseID, ResourceInputs: inputs})
			requireErrorCode(t, err, ErrInvalidInput)
			if all := store.All(); len(all) != 0 {
				t.Errorf("refused entry inputs created sessions: %v", all)
			}
			if lines := f.lines(t); len(lines) != 0 {
				t.Errorf("an action ran with refused inputs: %q", lines)
			}
		})
	}
}

func TestUp_EntryInputsAreRecordedAndReachSubscribeAndUnsubscribe(t *testing.T) {
	store := testStore(t)
	f := newGatedFixture(t)

	res, err := Up(f.cfg, store, UpParams{Identifier: identityCaseID, ResourceInputs: map[string]any{"token": "entry"}})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	s := store.Get(res.SessionName)
	if s.ResourceInputs["token"] != "entry" {
		t.Errorf("recorded entry inputs = %v, want token=entry", s.ResourceInputs)
	}
	if _, ok := s.Inputs["token"]; ok {
		t.Errorf("session inputs = %v, want the entry inputs kept out of them", s.Inputs)
	}
	f.requireLine(t, "subscribe "+identityCaseID+" entry")

	if _, err := Destroy(f.cfg, store, DestroyParams{Identifier: res.SessionName}); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	f.requireLine(t, "unsubscribe "+identityCaseID+" entry")
}

func TestUp_EntryInputsSurviveAFailedSubscribeIntoItsRetry(t *testing.T) {
	store := testStore(t)
	f := newGatedFixture(t)
	if err := os.WriteFile(f.failFlag, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Up(f.cfg, store, UpParams{Identifier: identityCaseID, ResourceInputs: map[string]any{"token": "entry"}})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if lines := f.lines(t); len(lines) != 0 {
		t.Fatalf("action log = %q, want the failing subscribe to log nothing", lines)
	}
	if err := os.Remove(f.failFlag); err != nil {
		t.Fatal(err)
	}
	RetryPendingDelivery(f.cfg, store, res.SessionName)
	f.requireLine(t, "subscribe "+identityCaseID+" entry")
}

func TestUp_RejectsEntryInputsForAnExistingSession(t *testing.T) {
	store := testStore(t)
	f := newGatedFixture(t)
	if _, err := Up(f.cfg, store, UpParams{Identifier: identityCaseID, ResourceInputs: map[string]any{"token": "entry"}}); err != nil {
		t.Fatalf("up: %v", err)
	}
	_, err := Up(f.cfg, store, UpParams{Identifier: identityCaseID, ResourceInputs: map[string]any{"token": "other"}})
	requireErrorCode(t, err, ErrInvalidInput)
	if got := store.Get("case-1").ResourceInputs["token"]; got != "entry" {
		t.Errorf("entry inputs = %v after a refused re-supply, want them kept", got)
	}
}

func TestUp_EntryInputsForAResourceWithoutASchemaAreRefused(t *testing.T) {
	store := testStore(t)
	cfg := identityFixture(t)
	_, err := Up(cfg, store, UpParams{Identifier: identityCaseID, ResourceInputs: map[string]any{"token": "x"}})
	requireErrorCode(t, err, ErrInvalidInput)
	if all := store.All(); len(all) != 0 {
		t.Errorf("refused entry inputs created sessions: %v", all)
	}
}
