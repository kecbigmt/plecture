package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/service"
)

func TestWritePluginList_FormatsEditableAndFailedEntries(t *testing.T) {
	var buf bytes.Buffer
	err := writePluginList(&buf, []service.PluginListEntry{
		{ID: "local/okf", ResolvedRevision: "", NonReproducible: true, Status: "ok"},
		{ID: "official/github", ResolvedRevision: "abc123", Status: "plugin \"official/github\": no plect.lock entry"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "local/okf") || !strings.Contains(got, "(editable, non-reproducible)") {
		t.Errorf("editable entry not formatted as expected:\n%s", got)
	}
	if !strings.Contains(got, "abc123") || !strings.Contains(got, "no plect.lock entry") {
		t.Errorf("failed entry not formatted as expected:\n%s", got)
	}
}

func TestWritePluginVerify_ReportsStatusPerEntry(t *testing.T) {
	var buf bytes.Buffer
	err := writePluginVerify(&buf, &service.PluginVerifyResult{
		AllOK: false,
		Entries: []service.PluginVerifyEntry{
			{ID: "local/okf", OK: true},
			{ID: "local/editable-one", NonReproducible: true},
			{ID: "official/broken", OK: false, Error: "content hash mismatch"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"local/okf", "ok", "local/editable-one", "non-reproducible (editable)", "official/broken", "FAILED: content hash mismatch"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s", want, got)
		}
	}
}
