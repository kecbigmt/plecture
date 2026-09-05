package pullquery

import "testing"

func TestParseInputs_Valid(t *testing.T) {
	in, err := ParseInputs(`["acme/widgets"]`, `["agent-review"]`, "open", "true")
	if err != nil {
		t.Fatalf("ParseInputs: %v", err)
	}
	want := Inputs{Repositories: []string{"acme/widgets"}, Labels: []string{"agent-review"}, State: "open", Draft: true}
	if len(in.Repositories) != 1 || in.Repositories[0] != want.Repositories[0] ||
		len(in.Labels) != 1 || in.Labels[0] != want.Labels[0] ||
		in.State != want.State || in.Draft != want.Draft {
		t.Errorf("ParseInputs = %+v, want %+v", in, want)
	}
}

func TestParseInputs_DraftFalse(t *testing.T) {
	in, err := ParseInputs(`[]`, `[]`, "open", "false")
	if err != nil {
		t.Fatalf("ParseInputs: %v", err)
	}
	if in.Draft {
		t.Error("Draft = true, want false")
	}
}

func TestParseInputs_MalformedRepositoriesJSON(t *testing.T) {
	if _, err := ParseInputs(`not-json`, `[]`, "open", "false"); err == nil {
		t.Fatal("want an error for malformed --repositories JSON")
	}
}

func TestParseInputs_MalformedLabelsJSON(t *testing.T) {
	if _, err := ParseInputs(`[]`, `not-json`, "open", "false"); err == nil {
		t.Fatal("want an error for malformed --labels JSON")
	}
}

func TestParseInputs_InvalidState(t *testing.T) {
	if _, err := ParseInputs(`[]`, `[]`, "merged", "false"); err == nil {
		t.Fatal("want an error for an unsupported state value")
	}
}

// TestParseInputs_DraftMustBeExactlyTrueOrFalse pins the shape the ADR's
// query.poll/query.subscribe sketch renders: `{ expr = "inputs.draft ?
// 'true' : 'false'" }` never produces anything else, so any other value
// (including Go's flag.Bool-style "1"/"0" or an empty default) is a
// misrendered action rather than a valid input.
func TestParseInputs_DraftMustBeExactlyTrueOrFalse(t *testing.T) {
	for _, bad := range []string{"", "1", "0", "True", "yes"} {
		if _, err := ParseInputs(`[]`, `[]`, "open", bad); err == nil {
			t.Errorf("ParseInputs with --draft %q: want an error", bad)
		}
	}
}
