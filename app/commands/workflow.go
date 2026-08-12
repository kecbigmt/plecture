package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cradel-dev/cradel/app/internal/config"
	"github.com/cradel-dev/cradel/app/internal/service"
)

var (
	workflowListJSON     bool
	workflowListNoHeader bool
	workflowShowJSON     bool
)

var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Discover available workflows",
}

var workflowListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List available workflows",
	Long: `List workflows discoverable from the cascade (global + ancestor .sennit/workflows/).

Default output is a space-aligned table with ID / NAME / DESCRIPTION columns.
Use --json for a fully structured listing; --no-header switches to tab-separated
output without a header row so the result is consumable by cut/awk/etc.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		workflows, err := service.WorkflowList(cfg, cwd)
		if err != nil {
			return err
		}
		if workflowListJSON {
			b, err := json.MarshalIndent(workflows, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(b))
			return nil
		}
		if len(workflows) == 0 {
			fmt.Fprintln(os.Stderr, "No workflows found")
			return nil
		}
		return writeWorkflowList(os.Stdout, workflows, workflowListNoHeader)
	},
}

// writeWorkflowList renders the list result. --no-header switches from
// tabwriter space-padding to literal tab separators so the bytes are
// machine-consumable; the human form keeps the same `(no description)` /
// `(no name)` sentinels so a blank column doesn't look like a glitch.
func writeWorkflowList(out io.Writer, workflows []service.WorkflowSummary, noHeader bool) error {
	if noHeader {
		for _, wf := range workflows {
			if _, err := fmt.Fprintf(out, "%s\t%s\t%s\n", wf.ID, wf.Name, wf.Description); err != nil {
				return err
			}
		}
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tDESCRIPTION")
	for _, wf := range workflows {
		name := wf.Name
		if name == "" {
			name = "(no name)"
		}
		desc := wf.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", wf.ID, name, desc)
	}
	return w.Flush()
}

var workflowShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show full information about a workflow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		detail, err := service.WorkflowShow(cfg, cwd, args[0])
		if err != nil {
			return err
		}
		if workflowShowJSON {
			b, err := json.MarshalIndent(detail, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(b))
			return nil
		}
		return printWorkflowDetail(os.Stdout, detail)
	},
}

func printWorkflowDetail(out io.Writer, d *service.WorkflowDetail) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "ID:\t%s\n", d.ID)
	if d.Name != "" {
		fmt.Fprintf(w, "Name:\t%s\n", d.Name)
	}
	if d.Description != "" {
		fmt.Fprintf(w, "Description:\t%s\n", d.Description)
	}
	if d.Provider != "" {
		fmt.Fprintf(w, "Provider:\t%s\n", d.Provider)
	}
	if !d.AutoSelect {
		fmt.Fprintln(w, "Auto-select:\tfalse")
	}
	if d.ProviderInfo != nil {
		if d.ProviderInfo.Match != "" {
			fmt.Fprintf(w, "Resolver:\t%s → %s\n", d.ProviderInfo.Match, d.ProviderInfo.Name)
		}
		var hooks []string
		if d.ProviderInfo.Setup != "" {
			hooks = append(hooks, "setup")
		}
		if d.ProviderInfo.Cleanup != "" {
			hooks = append(hooks, "cleanup")
		}
		if len(hooks) > 0 {
			fmt.Fprintf(w, "Provider hooks:\t%s (use --json for the scripts)\n", strings.Join(hooks, ", "))
		}
	}
	if d.Tick != nil {
		if len(d.Tick.On) > 0 {
			fmt.Fprintf(w, "Tick on:\t%s\n", strings.Join(d.Tick.On, ", "))
		}
		if d.Tick.Heartbeat.Duration > 0 {
			fmt.Fprintf(w, "Tick heartbeat:\t%s\n", d.Tick.Heartbeat.Duration)
		}
	}
	if len(d.Display) > 0 {
		keys := make([]string, 0, len(d.Display))
		for k := range d.Display {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "Display %s:\t%s\n", k, d.Display[k])
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}

	if len(d.InputsSchema) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Inputs:")
		writeInputsSummary(out, d.InputsSchema)
	}

	if len(d.Nodes) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Nodes (execute order):")
		writeNodesByScope(out, d.Nodes)
	}

	if len(d.Channels) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Event channels:")
		writeChannels(out, d.Channels)
	}
	return nil
}

// writeChannels lists each [[event.channel]] with the primitive it resolves to
// and the event types it delivers, matching the indented outline of the Nodes
// section above.
func writeChannels(out io.Writer, channels []service.WorkflowChannel) {
	for _, ch := range channels {
		line := "  " + ch.Name + " (uses " + ch.Uses
		if ch.Type != "" {
			line += ", " + ch.Type
		}
		line += ")"
		fmt.Fprintln(out, line)
		if len(ch.Include) > 0 {
			fmt.Fprintf(out, "    delivers: %s\n", strings.Join(ch.Include, ", "))
		}
	}
}

// writeNodesByScope groups the topo-sorted node list by scope. Both the scope
// header and each node id are indented under the `Nodes (execute order):`
// heading so the whole listing reads as one nested outline — matching the
// `Inputs:` section above (which also uses pure indent rather than bullets).
// StreamReporter's flush-left scope headers earn their position by sharing a
// column with status icons during a run; `show` has no icons, so the indented
// form reads cleaner here. Deps are omitted on purpose; order is the signal.
func writeNodesByScope(out io.Writer, nodes []service.WorkflowNode) {
	currentScope := ""
	for _, n := range nodes {
		if n.Scope != currentScope {
			fmt.Fprintf(out, "  %s:\n", n.Scope)
			currentScope = n.Scope
		}
		line := "    " + n.ID
		if n.Uses != "" && n.Uses != n.ID {
			line += " (uses " + n.Uses + ")"
		}
		fmt.Fprintln(out, line)
	}
}

// writeInputsSummary renders the top-level `properties` of a JSON Schema as
// human-readable bullets. allOf-merged schemas (cascade overlay) are flattened
// across their constituents so the reader sees a single list.
func writeInputsSummary(out io.Writer, schema map[string]any) {
	keys := collectSchemaKeys(schema)
	if len(keys) == 0 {
		fmt.Fprintln(out, "  (no inputs)")
		return
	}
	required := collectRequired(schema)
	props := collectProperties(schema)
	sort.Strings(keys)
	for _, k := range keys {
		prop, _ := props[k].(map[string]any)
		typ := stringValue(prop, "type")
		if typ == "" {
			typ = "any"
		}
		flag := "optional"
		if required[k] {
			flag = "required"
		}
		fmt.Fprintf(out, "  %s (%s, %s)\n", k, typ, flag)
		if desc := stringValue(prop, "description"); desc != "" {
			fmt.Fprintf(out, "    %s\n", desc)
		}
		if exs := stringListValue(prop, "examples"); len(exs) > 0 {
			fmt.Fprintf(out, "    Examples: %s\n", strings.Join(exs, ", "))
		}
		if enum := stringListValue(prop, "enum"); len(enum) > 0 {
			fmt.Fprintf(out, "    Allowed: %s\n", strings.Join(enum, ", "))
		}
	}
}

// collectProperties walks an inputs_schema (including allOf cascades) and
// returns the union of `properties` maps. Later layers don't override earlier
// ones — we only flatten so the reader sees every declared key.
func collectProperties(schema map[string]any) map[string]any {
	out := map[string]any{}
	for _, layer := range schemaLayers(schema) {
		if props, ok := layer["properties"].(map[string]any); ok {
			for k, v := range props {
				if _, exists := out[k]; !exists {
					out[k] = v
				}
			}
		}
	}
	return out
}

func collectRequired(schema map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, layer := range schemaLayers(schema) {
		if req, ok := layer["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					out[s] = true
				}
			}
		}
	}
	return out
}

func collectSchemaKeys(schema map[string]any) []string {
	props := collectProperties(schema)
	out := make([]string, 0, len(props))
	for k := range props {
		out = append(out, k)
	}
	return out
}

// schemaLayers returns the schema itself plus any allOf entries (cascade
// overlay flattens layers as `allOf` in combineInputsSchemas).
func schemaLayers(schema map[string]any) []map[string]any {
	out := []map[string]any{schema}
	if all, ok := schema["allOf"].([]any); ok {
		for _, entry := range all {
			if m, ok := entry.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

func stringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func stringListValue(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		switch x := v.(type) {
		case string:
			out = append(out, x)
		default:
			out = append(out, fmt.Sprintf("%v", x))
		}
	}
	return out
}

func init() {
	workflowListCmd.Flags().BoolVar(&workflowListJSON, "json", false, "Output as JSON array")
	workflowListCmd.Flags().BoolVar(&workflowListNoHeader, "no-header", false, "Omit the header row")

	workflowShowCmd.Flags().BoolVar(&workflowShowJSON, "json", false, "Output as JSON")

	workflowCmd.AddCommand(workflowListCmd)
	workflowCmd.AddCommand(workflowShowCmd)
	rootCmd.AddCommand(workflowCmd)
}
