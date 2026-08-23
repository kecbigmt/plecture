package lang

import (
	"strings"
	"testing"
)

// A definition naming an executable can only be judged against a resolver, so
// validating one without a resolver is a caller mistake. It must say so:
// reaching a nil interface leaves a stack trace where a sentence belongs.
func TestValidateDefinition_BinReferenceWithoutAResolverIsReported(t *testing.T) {
	defs, err := ParseDefinitionDocument("channels/delivery.toml", []byte(`
[delivery]
kind = "channel"
type = "exec"
bin  = "deliver"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("parsed %d definitions, want 1", len(defs))
	}

	err = Validation{}.ValidateDefinition(defs[0])
	if err == nil {
		t.Fatal("validating a bin reference with no resolver must be reported, not accepted")
	}
	if !strings.Contains(err.Error(), "deliver") {
		t.Errorf("error = %v, want it to name the unresolvable reference", err)
	}
}
