package config

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/domain"
)

func pinnedSession(t *testing.T, body string) *domain.Session {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	return &domain.Session{Name: "s", Workflow: "work", WorkflowRevision: hex.EncodeToString(sum[:]), WorkflowRevisionBody: body}
}

func TestRetiredRevisionSpellings(t *testing.T) {
	current, _, err := WorkflowRevision{
		Workflow: WorkflowFile{Address: "work", Nodes: []WorkflowNode{{ID: "one", Uses: "effect"}}},
		Effects:  map[string]TaskDefinition{"effect": {ID: "effect", Scope: TaskScopeSession}},
		Tasks:    map[string]TaskDocument{"task": {ID: "task", Resource: "issue", ExtendsLayers: []TaskDocument{{ID: "base", Resource: "issue"}}}},
	}.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"a revision written in the current dialect", string(current), nil},
		{
			"a workflow naming a workspace provider",
			`{"workflow":{"Address":"work","WorkspaceProvider":"forge","WorkspaceProviderInputs":null},"effects":{},"tasks":{}}`,
			[]string{"workspace_provider"},
		},
		{
			// A previous release encoded every workflow field, so an empty
			// provider name still marks the revision as written in that dialect.
			"a workflow encoded with an empty provider name",
			`{"workflow":{"Address":"work","WorkspaceProvider":""},"effects":{},"tasks":{}}`,
			[]string{"workspace_provider"},
		},
		{
			"the @workflow pseudo-node's provider declaration",
			`{"workflow":{"Address":"work"},"effects":{},"tasks":{},"provider":{"ID":"forge"}}`,
			[]string{"@workflow"},
		},
		{
			"a pending workspace capture",
			`{"workflow":{"Address":"work"},"effects":{},"tasks":{},"workspace_capture_pending":true}`,
			[]string{"workspace_provider"},
		},
		{
			"a task document written for a resource observer",
			`{"workflow":{"Address":"work"},"effects":{},"tasks":{"review":{"ID":"review","ResourceObserver":"issue"}}}`,
			[]string{"resource_observer"},
		},
		{
			"an extends layer written for a resource observer",
			`{"workflow":{"Address":"work"},"effects":{},"tasks":{"review":{"ID":"review","ExtendsLayers":[{"ID":"base","ResourceObserver":"issue"}]}}}`,
			[]string{"resource_observer"},
		},
		{
			"every retired spelling at once, reported once each in a stable order",
			`{"workflow":{"Address":"work","WorkspaceProvider":"forge"},"effects":{},"tasks":{"a":{"ResourceObserver":"issue"},"b":{"ResourceObserver":"issue"}},"provider":{"ID":"forge"}}`,
			[]string{"@workflow", "resource_observer", "workspace_provider"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RetiredRevisionSpellings(pinnedSession(t, tt.body)); !slices.Equal(got, tt.want) {
				t.Fatalf("RetiredRevisionSpellings = %v, want %v", got, tt.want)
			}
		})
	}
	t.Run("a session without a stored revision", func(t *testing.T) {
		if got := RetiredRevisionSpellings(&domain.Session{Name: "s", Workflow: "work"}); got != nil {
			t.Fatalf("RetiredRevisionSpellings = %v, want none", got)
		}
	})
	t.Run("an unparseable body is left to the decoder to report", func(t *testing.T) {
		if got := RetiredRevisionSpellings(pinnedSession(t, `{`)); got != nil {
			t.Fatalf("RetiredRevisionSpellings = %v, want none", got)
		}
	})
}
