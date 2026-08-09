package domain

import (
	"slices"
	"testing"
)

func TestRelationFromTarget(t *testing.T) {
	sessions := map[string]*Session{
		"root":        {Name: "root", Children: []string{"work", "review"}},
		"work":        {Name: "work", ParentSession: "root", Children: []string{"child"}},
		"review":      {Name: "review", ParentSession: "root"},
		"child":       {Name: "child", ParentSession: "work"},
		"grandchild":  {Name: "grandchild", ParentSession: "child"},
		"independent": {Name: "independent"},
	}

	tests := []struct {
		name     string
		target   string
		other    string
		relation SessionRelation
	}{
		{"self", "work", "work", RelationSelf},
		{"parent", "work", "root", RelationParent},
		{"child", "work", "child", RelationChild},
		{"sibling", "work", "review", RelationSibling},
		{"ancestor", "grandchild", "work", RelationAncestor},
		{"descendant", "work", "grandchild", RelationDescendant},
		{"unrelated", "work", "independent", RelationUnrelated},
		{"missing", "work", "missing", RelationUnrelated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RelationFromTarget(sessions, tt.target, tt.other)
			if got != tt.relation {
				t.Fatalf("RelationFromTarget(%q, %q) = %q, want %q", tt.target, tt.other, got, tt.relation)
			}
		})
	}
}

// A reviewer opted into a parentless session's implicit root (root:X) is that
// session's sibling; two unrelated parentless sessions do not share a root.
func TestRelationFromTargetImplicitRoot(t *testing.T) {
	sessions := map[string]*Session{
		"x":         {Name: "x"},
		"reviewer":  {Name: "reviewer", ParentSession: "root:x"},
		"y":         {Name: "y"},
		"reviewery": {Name: "reviewery", ParentSession: "root:y"},
	}

	tests := []struct {
		name     string
		target   string
		other    string
		relation SessionRelation
	}{
		{"opted-in reviewer is sibling of root target", "x", "reviewer", RelationSibling},
		{"sibling relation is symmetric", "reviewer", "x", RelationSibling},
		{"separate parentless sessions stay unrelated", "x", "y", RelationUnrelated},
		{"reviewer of a different root is unrelated to x", "reviewer", "y", RelationUnrelated},
		{"reviewers of different roots are unrelated to each other", "reviewer", "reviewery", RelationUnrelated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RelationFromTarget(sessions, tt.target, tt.other)
			if got != tt.relation {
				t.Fatalf("RelationFromTarget(%q, %q) = %q, want %q", tt.target, tt.other, got, tt.relation)
			}
		})
	}
}

func TestSubtree(t *testing.T) {
	sessions := map[string]*Session{
		"root":        {Name: "root", Children: []string{"work", "review"}},
		"work":        {Name: "work", ParentSession: "root", Children: []string{"child"}},
		"review":      {Name: "review", ParentSession: "root"},
		"child":       {Name: "child", ParentSession: "work", Children: []string{"grandchild"}},
		"grandchild":  {Name: "grandchild", ParentSession: "child"},
		"independent": {Name: "independent"},
	}

	tests := []struct {
		name string
		root string
		want []string
	}{
		{"full tree from root", "root", []string{"child", "grandchild", "review", "root", "work"}},
		{"subtree from interior node", "work", []string{"child", "grandchild", "work"}},
		{"leaf is its own subtree", "grandchild", []string{"grandchild"}},
		{"unrelated node alone", "independent", []string{"independent"}},
		{"missing root yields nil", "absent", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Subtree(sessions, tt.root)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("Subtree(%q) = %v, want %v", tt.root, got, tt.want)
			}
		})
	}
}

// A malformed map with a parent cycle must not loop and must not pull cyclic
// nodes into an unrelated root's subtree.
func TestSubtreeToleratesParentCycle(t *testing.T) {
	sessions := map[string]*Session{
		"root": {Name: "root"},
		"a":    {Name: "a", ParentSession: "b"},
		"b":    {Name: "b", ParentSession: "a"},
	}
	if got := Subtree(sessions, "root"); !slices.Equal(got, []string{"root"}) {
		t.Fatalf("Subtree(root) = %v, want [root]", got)
	}
}
