package service

import (
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

// addPlainResource declares a resource whose instance name is the identifier's
// own tail, so a test can choose an instance name that contains the tag
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

func TestUp_UntaggedSessionNameIsTheResourceInstanceName(t *testing.T) {
	store := testStore(t)
	cfg := identityFixture(t)

	res, err := Up(cfg, store, UpParams{Identifier: identityCaseID})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if res.SessionName != "case-1" {
		t.Errorf("session = %q, want the instance name case-1", res.SessionName)
	}
}

func TestUp_TagAppendsOneSegmentToTheInstanceName(t *testing.T) {
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
	if got.ResourceType != "case" || got.ResourceID != identityCaseID || got.InstanceName != "case-1" || got.Tag != "review" {
		t.Errorf("entry = (type %q, id %q, instance %q, tag %q), want (case, %s, case-1, review)", got.ResourceType, got.ResourceID, got.InstanceName, got.Tag, identityCaseID)
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
		t.Errorf("tag = %q, want none: the instance name itself contains the separator", status.Identity.Tag)
	}
	if status.Identity.ResourceType != "plain" || status.Identity.InstanceName != "a+t" {
		t.Errorf("identity = (type %q, instance %q), want (plain, a+t)", status.Identity.ResourceType, status.Identity.InstanceName)
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
			name: "different resource type",
			seed: func(t *testing.T, cfg *config.Config, store *state.Store) {
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
			},
			params: UpParams{Identifier: "alt://x"},
			want:   `resource "case"`,
		},
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
				if after[name].ResourceID != session.ResourceID || after[name].Workflow != session.Workflow {
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
	if err := store.Put(&domain.Session{Name: "case-1", Workflow: "flow", ResourceType: "alt", InstanceName: "case-1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := resolveSession(cfg, store, identityCaseID); err == nil {
		t.Fatal("a case identifier resolved to a session recorded for another resource type")
	}
}
