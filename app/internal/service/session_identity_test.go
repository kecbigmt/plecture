package service

import (
	contract "github.com/kecbigmt/plecture/contracts/state"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
)

const identityCaseID = "case://1"

const caseResourceDoc = `[case]
kind  = "resource"
match = '^case://(?P<n>[0-9]+)$'
name  = { expr = "'case-' + match.n" }

[case.observe]
type   = "shell"
script = "echo '{}'"
`

func identityFixture(t *testing.T) *config.Config {
	t.Helper()
	cfg := writeWorkflowFixture(t, "flow",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	useResource(t, cfg, "flow", "case", caseResourceDoc)
	return cfg
}

// addPlainResource declares a resource whose name is the identifier's
// own tail, so a test can choose a resource name that contains the tag
// separator.
func addPlainResource(t *testing.T, cfg *config.Config) {
	t.Helper()
	writeResourceDoc(t, cfg, "plain", `[plain]
kind  = "resource"
match = '^plain://(?P<rest>[a-z0-9+]+)$'
name  = { expr = "match.rest" }

[plain.observe]
type   = "shell"
script = "echo '{}'"
`)
	addFixtureWorkflow(t, cfg, "plain_wf", "plain", "noop", "")
}

func TestUp_UntaggedSessionNameIsTheResourceName(t *testing.T) {
	store := testStore(t)
	cfg := identityFixture(t)

	res, err := Up(cfg, store, UpParams{Identifier: identityCaseID})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if res.SessionName != "case-1" {
		t.Errorf("session = %q, want the resource name case-1", res.SessionName)
	}
}

func TestUp_TagAppendsOneSegmentToTheResourceName(t *testing.T) {
	store := testStore(t)
	cfg := identityFixture(t)

	res, err := Up(cfg, store, UpParams{Identifier: identityCaseID, Tag: "review"})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if res.SessionName != "case-1+review" {
		t.Errorf("session = %q, want case-1+review", res.SessionName)
	}
}

func TestUp_RejectsATagThatIsNotOneSegment(t *testing.T) {
	cfg := identityFixture(t)
	for _, tag := range []string{"a+b", "a/b", "a.b", "a b"} {
		t.Run(tag, func(t *testing.T) {
			store := testStore(t)
			_, err := Up(cfg, store, UpParams{Identifier: identityCaseID, Tag: tag})
			svcErr, ok := err.(*Error)
			if !ok || svcErr.Code != ErrInvalidTag {
				t.Fatalf("err = %v, want %s", err, ErrInvalidTag)
			}
			if len(store.All()) != 0 {
				t.Errorf("a rejected tag created sessions: %v", store.All())
			}
		})
	}
}

func TestUp_SameEntryResourceAndTagReuseTheSession(t *testing.T) {
	for _, tag := range []string{"", "review"} {
		t.Run("tag="+tag, func(t *testing.T) {
			store := testStore(t)
			cfg := identityFixture(t)
			first, err := Up(cfg, store, UpParams{Identifier: identityCaseID, Tag: tag})
			if err != nil {
				t.Fatalf("first up: %v", err)
			}
			second, err := Up(cfg, store, UpParams{Identifier: identityCaseID, Tag: tag})
			if err != nil {
				t.Fatalf("second up: %v", err)
			}
			if second.SessionName != first.SessionName || len(store.All()) != 1 {
				t.Errorf("second up = %q with %d sessions, want the one session %q", second.SessionName, len(store.All()), first.SessionName)
			}
		})
	}
}

func TestUp_RecordsTheEntryResourceOnTheSession(t *testing.T) {
	store := testStore(t)
	cfg := identityFixture(t)

	if _, err := Up(cfg, store, UpParams{Identifier: identityCaseID, Tag: "review"}); err != nil {
		t.Fatalf("up: %v", err)
	}
	got := store.Get("case-1+review")
	if got == nil {
		t.Fatal("session not recorded")
	}
	want := contract.Resource{Type: "case", Name: "case-1", Locator: identityCaseID}
	if got.Resource == nil || got.Resource.ID == "" || got.Tag != "review" {
		t.Fatalf("entry = (%+v, tag %q), want a recorded resource and tag review", got.Resource, got.Tag)
	}
	if recorded := *got.Resource; recorded.Type != want.Type || recorded.Name != want.Name || recorded.Locator != want.Locator {
		t.Errorf("resource = %+v, want %+v", recorded, want)
	}
}

func TestStatus_ReportsTheRecordedTagRatherThanParsingTheName(t *testing.T) {
	store := testStore(t)
	cfg := identityFixture(t)
	addPlainResource(t, cfg)

	if _, err := Up(cfg, store, UpParams{Identifier: "plain://a+t"}); err != nil {
		t.Fatalf("up: %v", err)
	}
	status, err := Status(cfg, store, "a+t")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Identity.Tag != "" {
		t.Errorf("tag = %q, want none: the resource name itself contains the separator", status.Identity.Tag)
	}
	if status.Identity.Resource == nil || status.Identity.Resource.Type != "plain" || status.Identity.Resource.Name != "a+t" {
		t.Errorf("resource = %+v, want plain resource a+t", status.Identity.Resource)
	}
}

func TestUp_SameSessionNameWithADifferentEntryIsACollision(t *testing.T) {
	tests := []struct {
		name   string
		seed   func(t *testing.T, cfg *config.Config, store *state.Store)
		params UpParams
		want   string
	}{
		{
			name: "different tag",
			seed: func(t *testing.T, cfg *config.Config, store *state.Store) {
				addPlainResource(t, cfg)
				mustUp(t, cfg, store, UpParams{Identifier: "plain://a+t"})
			},
			params: UpParams{Identifier: "plain://a", Tag: "t"},
			want:   "no tag",
		},
		{
			name: "no recorded entry",
			seed: func(t *testing.T, cfg *config.Config, store *state.Store) {
				now := time.Now()
				if err := store.Put(&domain.Session{Name: "case-1", Workflow: "flow", CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
			},
			params: UpParams{Identifier: identityCaseID},
			want:   "records no entry resource",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testStore(t)
			cfg := identityFixture(t)
			tt.seed(t, cfg, store)
			before := store.All()

			_, err := Up(cfg, store, tt.params)
			if err == nil || !strings.Contains(err.Error(), "collides") || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want a collision naming %s", err, tt.want)
			}
			after := store.All()
			if len(after) != len(before) {
				t.Fatalf("sessions = %d after a collision, want %d", len(after), len(before))
			}
			for name, session := range before {
				if domain.Locator(after[name].Resource) != domain.Locator(session.Resource) || after[name].Workflow != session.Workflow {
					t.Errorf("collision changed session %q: %+v -> %+v", name, session, after[name])
				}
			}
		})
	}
}

func mustUp(t *testing.T, cfg *config.Config, store *state.Store, params UpParams) {
	t.Helper()
	if _, err := Up(cfg, store, params); err != nil {
		t.Fatalf("seed up %s: %v", params.Identifier, err)
	}
}

func TestResolveSession_ResourceIdentifierDoesNotReachAnotherEntrysSession(t *testing.T) {
	store := testStore(t)
	cfg := identityFixture(t)
	now := time.Now()
	if err := store.Put(&domain.Session{Name: "case-1", Workflow: "flow", Resource: testResource("alt://1"), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := resolveSession(cfg, store, identityCaseID); err == nil {
		t.Fatal("a case identifier resolved to a session recorded for another resource type")
	}
}

func TestResolveSessionName_ResourceIdentifierDoesNotNameAnotherEntrysSession(t *testing.T) {
	store := testStore(t)
	cfg := identityFixture(t)
	now := time.Now()
	if err := store.Put(&domain.Session{Name: "case-1", Workflow: "flow", Resource: testResource("alt://1"), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	name, err := resolveSessionName(cfg, store, identityCaseID)
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("resolveSessionName = (%q, %v), want a collision instead of naming another entry's session", name, err)
	}
}

func addSpellingResource(t *testing.T, cfg *config.Config) {
	t.Helper()
	writeResourceDoc(t, cfg, "spell", `[spell]
kind  = "resource"
match = '^spell://(?P<n>[0-9]+)(/reply)?$'
name  = { expr = "'spell-' + match.n" }

[spell.observe]
type   = "shell"
script = "echo '{}'"
`)
	addFixtureWorkflow(t, cfg, "spell_wf", "spell", "noop", "")
}

func TestUp_TwoSpellingsOfOneResourceReachOneSessionAndOneRecord(t *testing.T) {
	store := testStore(t)
	cfg := identityFixture(t)
	addSpellingResource(t, cfg)

	first, err := Up(cfg, store, UpParams{Identifier: "spell://1"})
	if err != nil {
		t.Fatalf("first up: %v", err)
	}
	second, err := Up(cfg, store, UpParams{Identifier: "spell://1/reply"})
	if err != nil {
		t.Fatalf("second up through another spelling: %v", err)
	}
	if first.SessionName != "spell-1" || second.SessionName != first.SessionName || len(store.All()) != 1 {
		t.Fatalf("sessions = %q then %q (%d live), want the one session spell-1", first.SessionName, second.SessionName, len(store.All()))
	}
	recorded, err := store.ResourceByName("spell-1")
	if err != nil || recorded == nil {
		t.Fatalf("resource spell-1 = %v, %v", recorded, err)
	}
	if recorded.Locator != "spell://1" {
		t.Errorf("locator = %q, want the first spelling", recorded.Locator)
	}
	if session := store.Get("spell-1"); session.Resource == nil || session.Resource.ID != recorded.ID {
		t.Errorf("session references %+v, want the recorded row %s", session.Resource, recorded.ID)
	}
}

func TestUp_ANameAnotherResourceTypeHoldsIsRefusedWhenResolved(t *testing.T) {
	store := testStore(t)
	cfg := identityFixture(t)
	writeResourceDoc(t, cfg, "alt", `[alt]
kind  = "resource"
match = '^alt://'
name  = { expr = "'case-1'" }

[alt.observe]
type   = "shell"
script = "echo '{}'"
`)
	addFixtureWorkflow(t, cfg, "alt_wf", "alt", "noop", "")
	mustUp(t, cfg, store, UpParams{Identifier: identityCaseID})

	_, err := Up(cfg, store, UpParams{Identifier: "alt://x", Tag: "other"})
	if err == nil || !strings.Contains(err.Error(), `already holds`) || !strings.Contains(err.Error(), `resource "case"`) {
		t.Fatalf("err = %v, want the name refused because resource case holds it", err)
	}
	if len(store.All()) != 1 {
		t.Errorf("sessions = %v, want only the case session", store.All())
	}
}

func TestUp_ADerivedNameOutsideTheSessionNameRuleIsRefused(t *testing.T) {
	store := testStore(t)
	cfg := identityFixture(t)
	writeResourceDoc(t, cfg, "dotted", `[dotted]
kind  = "resource"
match = '^dotted://(?P<n>[0-9]+)$'
name  = { expr = "'dotted.' + match.n" }

[dotted.observe]
type   = "shell"
script = "echo '{}'"
`)
	addFixtureWorkflow(t, cfg, "dotted_wf", "dotted", "noop", "")

	_, err := Up(cfg, store, UpParams{Identifier: "dotted://1"})
	if err == nil || !strings.Contains(err.Error(), `"dotted.1"`) || !strings.Contains(err.Error(), "not a valid resource name") {
		t.Fatalf("err = %v, want the derived name dotted.1 refused", err)
	}
	if len(store.All()) != 0 {
		t.Errorf("sessions = %v, want none", store.All())
	}
}

func TestUp_SessionTagIsTheRecordedTagWhenTheResourceNameHoldsTheSeparator(t *testing.T) {
	store := testStore(t)
	cfg := identityFixture(t)
	addPlainResource(t, cfg)
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, "tasks", "tagged.toml"), []byte(`[tagged]
kind  = "effect"
scope = "session"

[tagged.setup]
type   = "shell"
script = '''printf '{"tag":"%s"}' "$tag"'''

[tagged.setup.bind]
tag = { from = "session.tag", default = "" }

[tagged.health.alive]
type = "noop"

[tagged.outputs_schema]
type = "object"

[tagged.outputs_schema.properties.tag]
type = "string"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	addFixtureWorkflow(t, cfg, "plain_wf", "plain", "tagged", "")

	res, err := Up(cfg, store, UpParams{Identifier: "plain://a+b", Tag: "t"})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if res.SessionName != "a+b+t" {
		t.Fatalf("session = %q, want a+b+t", res.SessionName)
	}
	node := store.Get("a+b+t").Nodes["tagged"]
	if node == nil || node.Outputs["tag"] != "t" {
		t.Fatalf("tagged node = %+v, want session.tag to read the recorded tag t", node)
	}
}
