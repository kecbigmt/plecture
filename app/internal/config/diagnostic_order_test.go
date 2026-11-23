package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

const repeatedLoads = 50

func TestLoadReportsTheFirstErrorInKeyOrder(t *testing.T) {
	cases := []struct {
		name string
		load func(t *testing.T) error
		want string
	}{
		{
			name: "a channel input_schema declaring three unreadable names",
			load: func(t *testing.T) error {
				base := t.TempDir()
				writeChannelDoc(t, base, "broken", `
[c]
kind    = "channel"
type    = "exec"
command = "true"

[c.input_schema]
"gamma-x" = { type = "string" }
"alpha-x" = { type = "string" }
"beta-x"  = { type = "string" }
`)
				_, err := (&Config{BaseDir: base}).LoadChannels("")
				return err
			},
			want: `input_schema "alpha-x"`,
		},
		{
			name: "chain inputs each declaring both from and expr",
			load: func(t *testing.T) error {
				base := t.TempDir()
				writeFile(t, filepath.Join(base, "tasks", "t.toml"), `
[t]
kind         = "task"
resource     = "issue"
instructions = [{ text = "x" }]

[[t.chains]]
workflow = "w"

[t.chains.inputs]
gamma = { from = "session.name", expr = "1" }
alpha = { from = "session.name", expr = "1" }
beta  = { from = "session.name", expr = "1" }
`)
				_, err := (&Config{BaseDir: base}).LoadTaskDocuments("")
				return err
			},
			want: "t.chains[0].inputs.alpha:",
		},
		{
			name: "a workflow channel binding setting three undeclared inputs",
			load: func(*testing.T) error {
				return ValidateWorkflowChannels(
					WorkflowFile{Event: WorkflowEvent{Channel: []EventChannel{{
						Name: "runtime", Uses: "socket", Include: []string{"user.*"},
						Inputs: map[string]*lang.Value{"gamma": literalValue("x"), "alpha": literalValue("x"), "beta": literalValue("x")},
					}}}},
					map[string]ChannelDefinition{"socket": {Type: ChannelTypeExec}},
				)
			},
			want: `input "alpha"`,
		},
		{
			name: "a workflow channel binding leaving three required inputs unset",
			load: func(*testing.T) error {
				return ValidateWorkflowChannels(
					WorkflowFile{Event: WorkflowEvent{Channel: []EventChannel{{
						Name: "runtime", Uses: "socket", Include: []string{"user.*"},
					}}}},
					map[string]ChannelDefinition{"socket": {Type: ChannelTypeExec, InputSchema: map[string]ChannelInputSpec{
						"gamma": {Type: "string", Required: true},
						"alpha": {Type: "string", Required: true},
						"beta":  {Type: "string", Required: true},
					}}},
				)
			},
			want: `required input "alpha"`,
		},
		{
			name: "a revision capture freezing three effects' missing schema files",
			load: func(t *testing.T) error {
				base := t.TempDir()
				writeFile(t, filepath.Join(base, "workflows", "w.toml"), "[w]\nkind = \"workflow\"\nresource = \"issue\"\n")
				for _, id := range []string{"gamma", "alpha", "beta"} {
					writeFile(t, filepath.Join(base, "effects", id+".toml"),
						"["+id+"]\nkind = \"effect\"\nscope = \"run\"\ninputs_schema_file = \"missing-"+id+".json\"\n")
				}
				_, err := (&Config{BaseDir: base}).CaptureWorkflowRevision("", "w")
				return err
			},
			want: `effect "alpha"`,
		},
		{
			name: "a revision capture freezing three task documents' missing schema files",
			load: func(t *testing.T) error {
				base := t.TempDir()
				writeFile(t, filepath.Join(base, "workflows", "w.toml"), "[w]\nkind = \"workflow\"\nresource = \"issue\"\n")
				writeFile(t, filepath.Join(base, "resources", "issue.toml"), "[issue]\nkind = \"resource\"\nmatch = \"^issue:\"\nname = \"issue\"\n\n[issue.observe]\ntype = \"exec\"\ncommand = \"true\"\n")
				for _, id := range []string{"gamma", "alpha", "beta"} {
					writeFile(t, filepath.Join(base, "tasks", id+".toml"),
						"["+id+"]\nkind = \"task\"\nresource = \"issue\"\ninstructions = [{ text = \"x\" }]\ninputs_schema_file = \"missing-"+id+".json\"\n")
				}
				_, err := (&Config{BaseDir: base}).CaptureWorkflowRevision("", "w")
				return err
			},
			want: `task "alpha"`,
		},
		{
			name: "two channel files whose ids sort opposite to their paths",
			load: func(t *testing.T) error {
				base := t.TempDir()
				writeChannelDoc(t, base, "a", "[z]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\n\n[z.input_schema]\n\"alpha-x\" = { type = \"string\" }\n")
				writeChannelDoc(t, base, "z", "[a]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\n\n[a.input_schema]\n\"alpha-x\" = { type = \"string\" }\n")
				_, err := (&Config{BaseDir: base}).LoadChannels("")
				return err
			},
			want: filepath.Join("channels", "a.toml"),
		},
		{
			name: "two workflow files whose ids sort opposite to their paths",
			load: func(t *testing.T) error {
				base := t.TempDir()
				writeFile(t, filepath.Join(base, "workflows", "a.toml"), "[z]\nkind = \"workflow\"\nresource = \"issue\"\nalpha = 1\n")
				writeFile(t, filepath.Join(base, "workflows", "z.toml"), "[a]\nkind = \"workflow\"\nresource = \"issue\"\nalpha = 1\n")
				_, err := (&Config{BaseDir: base}).LoadWorkflows("")
				return err
			},
			want: filepath.Join("workflows", "a.toml"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i := range repeatedLoads {
				err := tc.load(t)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("load %d: want an error mentioning %s, got %v", i, tc.want, err)
				}
			}
		})
	}
}
