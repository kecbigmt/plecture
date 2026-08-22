package configlang

import "testing"

func testExecutables() *ExecutableRegistry {
	return NewExecutableRegistry(
		PluginExecutables{Alias: "official", Path: "okf", Names: []string{"okf-goal", "okf-bundle"}},
		PluginExecutables{Alias: "official", Path: "tmux", Names: []string{"tmux-pane"}},
	)
}

func TestResolveBinFromPluginOwnedConfig(t *testing.T) {
	r := testExecutables()
	from := Ownership{IsPlugin: true, Alias: "official", Path: "okf"}

	if _, err := r.ResolveBin("okf-goal", from); err != nil {
		t.Errorf("a bare name resolves against the containing plugin's own executables: %v", err)
	}
	wantDiag(t, mustErr(r.ResolveBin("okf-goal-absent", from)), CodeBinUnknown, LayerSemantic)
	wantDiag(t, mustErr(r.ResolveBin("official/tmux/tmux-pane", from)), CodeRefCrossPlugin, LayerSemantic)
}

func TestResolveBinFromUserOwnedConfig(t *testing.T) {
	r := testExecutables()

	if _, err := r.ResolveBin("official/tmux", Ownership{}); err != nil {
		t.Errorf("a plugin with one executable needs no executable segment: %v", err)
	}
	if _, err := r.ResolveBin("official/okf/okf-goal", Ownership{}); err != nil {
		t.Errorf("the qualified slash form resolves: %v", err)
	}
	wantDiag(t, mustErr(r.ResolveBin("okf-goal", Ownership{})), CodeRefAliasRequired, LayerSemantic)
	wantDiag(t, mustErr(r.ResolveBin("official/okf", Ownership{})), CodeBinUnknown, LayerSemantic)
	wantDiag(t, mustErr(r.ResolveBin("official/okf/absent", Ownership{})), CodeBinUnknown, LayerSemantic)
}

// TestResolveBinAmbiguousPluginPath pins that an ambiguous nested reading
// fails loudly rather than guessing which segment is the executable.
func TestResolveBinAmbiguousPluginPath(t *testing.T) {
	r := NewExecutableRegistry(
		PluginExecutables{Alias: "official", Path: "team", Names: []string{"agents"}},
		PluginExecutables{Alias: "official", Path: "team.agents", Names: []string{"only"}},
	)
	if _, err := r.ResolveBin("official/team/agents", Ownership{}); err == nil {
		t.Error("both readings resolve, so the reference is ambiguous")
	}
}

func mustErr(_ string, err error) error { return err }
