package pullquery

import "testing"

func TestMatches_RepositoryFilter(t *testing.T) {
	in := Inputs{Repositories: []string{"acme/widgets"}, State: "all"}
	pr := PullFact{Owner: "acme", Repo: "widgets", State: "open"}
	if !Matches(pr, in) {
		t.Error("want a match for a listed repository")
	}
	other := PullFact{Owner: "acme", Repo: "gadgets", State: "open"}
	if Matches(other, in) {
		t.Error("want no match for an unlisted repository")
	}
}

func TestMatches_EmptyRepositoriesAdmitsAny(t *testing.T) {
	in := Inputs{State: "all"}
	pr := PullFact{Owner: "acme", Repo: "widgets", State: "open"}
	if !Matches(pr, in) {
		t.Error("want a match when no repository filter is configured")
	}
}

func TestMatches_State(t *testing.T) {
	tests := []struct {
		name     string
		inState  string
		prState  string
		wantSame bool
	}{
		{"open matches open", "open", "open", true},
		{"open rejects closed", "open", "closed", false},
		{"all matches open", "all", "open", true},
		{"all matches closed", "all", "closed", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Inputs{Repositories: []string{"acme/widgets"}, State: tt.inState}
			pr := PullFact{Owner: "acme", Repo: "widgets", State: tt.prState}
			if got := Matches(pr, in); got != tt.wantSame {
				t.Errorf("Matches = %v, want %v", got, tt.wantSame)
			}
		})
	}
}

func TestMatches_Draft(t *testing.T) {
	in := Inputs{Repositories: []string{"acme/widgets"}, State: "all", Draft: false}
	ready := PullFact{Owner: "acme", Repo: "widgets", State: "open", Draft: false}
	if !Matches(ready, in) {
		t.Error("want a match for a non-draft pull request when draft = false")
	}
	draft := PullFact{Owner: "acme", Repo: "widgets", State: "open", Draft: true}
	if Matches(draft, in) {
		t.Error("want no match for a draft pull request when draft = false")
	}
}

func TestMatches_LabelsRequireAll(t *testing.T) {
	in := Inputs{Repositories: []string{"acme/widgets"}, State: "all", Labels: []string{"agent-review", "urgent"}}
	both := PullFact{Owner: "acme", Repo: "widgets", State: "open", Labels: []string{"agent-review", "urgent", "extra"}}
	if !Matches(both, in) {
		t.Error("want a match when every requested label is present")
	}
	one := PullFact{Owner: "acme", Repo: "widgets", State: "open", Labels: []string{"agent-review"}}
	if Matches(one, in) {
		t.Error("want no match when only some requested labels are present")
	}
}

func TestValidateState(t *testing.T) {
	for _, ok := range []string{"open", "closed", "all"} {
		if err := ValidateState(ok); err != nil {
			t.Errorf("ValidateState(%q): %v", ok, err)
		}
	}
	if err := ValidateState("merged"); err == nil {
		t.Error("want an error for an unsupported state value")
	}
}

func TestItemSchemaBoundary_RequiresOnlyResource(t *testing.T) {
	if len(ItemSchemaRequired) != 1 || ItemSchemaRequired[0] != "resource" {
		t.Errorf("ItemSchemaRequired = %v, want exactly [\"resource\"]", ItemSchemaRequired)
	}
}
