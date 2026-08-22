package configlang

import "testing"

func TestCheckExpressionAcceptsTheProfile(t *testing.T) {
	tests := []struct {
		name    string
		surface *Surface
		src     string
	}{
		{"conditional over event data", surfaceChannelDelivery, "event.body != '' ? event.body : event.summary"},
		{"the has macro", surfaceChannelDelivery, "has(event.metadata.url) ? event.summary + ' (' + event.metadata.url + ')' : event.summary"},
		{"string construction", surfaceChannelDelivery, "'[' + event.type + '] ' + (event.body != '' ? event.body : event.summary)"},
		{"comprehension macro over a contract field", surfaceEffectInner, "inputs.mcp_servers.map(s, s.name)"},
		{"filter and size", surfaceEffectInner, "size(inputs.mcp_servers.filter(s, s.name != ''))"},
		{"a standard conversion", surfaceEffectOutputsBind, "'pid-' + string(inner.outputs.pid)"},
		{"an admitted strings-extension function", surfaceEffectOutputsBind, "inputs.mcp_servers.map(s, s.name).join(',')"},
		{"another admitted strings-extension function", surfaceEffectInner, "inputs.branch.lowerAscii()"},
		{"live roots on a completion leaf", surfaceTaskCompletion, "self.state.verdict_revision == resource.state.revision"},
		{"named regex captures", surfaceProviderName, "match.owner + '/' + match.repo + '-' + match.number"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkExpression(tc.src, tc.surface, Position{}); err != nil {
				t.Errorf("%q: %v", tc.src, err)
			}
		})
	}
}

func TestCheckExpressionRejections(t *testing.T) {
	tests := []struct {
		name    string
		surface *Surface
		src     string
		code    Code
		layer   Layer
	}{
		{"unparseable", surfaceChannelDelivery, "event.body ? ", CodeCELSyntax, LayerCEL},
		{"a custom function", surfaceChannelDelivery, "bin('codex-exec-enqueue')", CodeCELCustomFunction, LayerCEL},
		{"a function from a later version of an admitted extension", surfaceEffectInner, "inputs.name.format([1])", CodeCELCustomFunction, LayerCEL},
		{"a function from an extension the profile does not admit", surfaceEffectInner, "math.greatest(inputs.a, inputs.b)", CodeCELCustomFunction, LayerCEL},
		{"a variable the site does not declare", surfaceChannelDelivery, "workflow.outputs.branch", CodeCELUnknownName, LayerCEL},
		{"a root the site does not offer", surfaceTaskCompletion, "resource.id == ''", CodeFromRoot, LayerStructural},
		{"a root the site does not offer, semantically", surfaceEffectOutputsBind, "inner.inputs.pid", CodeFromRoot, LayerStructural},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantDiag(t, checkExpression(tc.src, tc.surface, Position{}), tc.code, tc.layer)
		})
	}
}

// TestCheckExpressionKeepsTheRatifiedCutLine pins the accepted-invalid side
// of the type-check cut line: a computed leaf whose result type does not fit
// its field loads, because projecting a JSON Schema contract into CEL type
// information is the sanctioned first check to drop.
func TestCheckExpressionKeepsTheRatifiedCutLine(t *testing.T) {
	if err := checkExpression("self.state.verdict_revision + 1", surfaceTaskCompletion, Position{}); err != nil {
		t.Errorf("a contract-typed mismatch is accepted-invalid, not a load error: %v", err)
	}
	// An operation CEL itself rejects is on the enforced side: no contract
	// projection is needed to know it cannot work.
	wantDiag(t, checkExpression("1 + 'a'", surfaceTaskCompletion, Position{}), CodeCELType, LayerCEL)
}

func TestCheckExpressionIgnoresComprehensionVariables(t *testing.T) {
	if err := checkExpression("inputs.servers.exists(s, s.name == 'x')", surfaceEffectInner, Position{}); err != nil {
		t.Errorf("a comprehension variable is bound by the macro, not by the surface: %v", err)
	}
}
