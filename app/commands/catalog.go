package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/service"
)

var (
	catalogAddRevision string
	catalogAddSubdir   string
	catalogAddYes      bool

	catalogUpdateRevision string

	catalogRemoveYes bool

	catalogListJSON bool
)

var catalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "Manage registered plugin catalogs",
}

var catalogAddCmd = &cobra.Command{
	Use:   "add <alias> <source>",
	Short: "Register a catalog under a local alias",
	Long: `Resolve <source> (git+https://, git+ssh://, path://, or path+editable://),
show its resolved lock coordinate, description, and published plugin paths,
and ask for confirmation before registering it. A registered catalog is the
trust act itself: nothing is enabled from it yet — follow up with
"plect plugin add <alias>/<path>" for each plugin you want to use.

--subdir names a subdirectory of the fetched source that becomes the
catalog root, for a source whose catalog.toml does not sit at its own root
(for example, a monorepo that publishes a catalog from one subtree). The
trust space is bounded by that subdirectory, not the whole source.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCatalogAdd(cmd, args[0], args[1])
	},
}

var catalogUpdateCmd = &cobra.Command{
	Use:   "update <alias>",
	Short: "Repoint every plugin enabled from a catalog to a fresh snapshot",
	Long: `Fetch the newest matching snapshot of an already-registered catalog
(--revision is required for a git-sourced catalog; rejected for a
path-sourced one, which instead re-hashes its current content), and rewrite
a fresh lock entry for every currently enabled plugin from it. No auto-update:
every bump is explicit.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := service.CatalogUpdate(cmd.Context(), mustPluginPaths(), service.CatalogUpdateParams{
			Alias:    args[0],
			Revision: catalogUpdateRevision,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "updated %s: revision=%s plugins=%v\n", result.Alias, orNone(result.ResolvedRevision), result.UpdatedPlugins)
		return nil
	},
}

var catalogRemoveCmd = &cobra.Command{
	Use:   "remove <alias>",
	Short: "Unregister a catalog and disable everything enabled from it",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCatalogRemove(cmd, args[0])
	},
}

var catalogListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show registered catalogs, validation state, and enabled plugins",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := service.CatalogList(mustPluginPaths())
		if err != nil {
			return err
		}
		if catalogListJSON {
			b, err := json.MarshalIndent(entries, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}
		return writeCatalogList(cmd.OutOrStdout(), entries)
	},
}

func writeCatalogList(out io.Writer, entries []service.CatalogListEntry) error {
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ALIAS\tSOURCE\tSUBDIR\tREVISION\tSTATUS\tPLUGINS")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%v\n", e.Alias, e.Source, orNoneSubdir(e.Subdir), orNone(e.ResolvedRevision), e.Status, e.EnabledPlugins)
	}
	return w.Flush()
}

// runCatalogAdd is the interactive-first-seen-confirmation flow: fetch and
// validate first (PreviewCatalogAdd never writes anything), then decide
// whether the human has confirmed, then commit.
func runCatalogAdd(cmd *cobra.Command, alias, source string) error {
	paths := mustPluginPaths()
	params := service.CatalogAddParams{Alias: alias, Source: source, Subdir: catalogAddSubdir, Revision: catalogAddRevision}

	preview, fetched, err := service.PreviewCatalogAdd(cmd.Context(), paths, params)
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "catalog %q resolves to:\n  source:   %s\n  subdir:   %s\n  revision: %s\n  plugins:  %v\n",
		preview.Alias, preview.Source, orNoneSubdir(params.Subdir), orNone(preview.ResolvedRevision), preview.Plugins)
	if preview.Description != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  description: %s\n", preview.Description)
	}

	switch {
	case catalogAddYes:
		fmt.Fprintln(cmd.OutOrStdout(), "registering via --yes")
	case isInteractive():
		if !confirm(cmd, "Register this catalog?") {
			return fmt.Errorf("aborted: catalog not registered")
		}
	default:
		return fmt.Errorf("catalog %q: re-run interactively to confirm, or pass --yes", alias)
	}

	added, err := service.CommitCatalogAdd(paths, params, fetched)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "registered %s: revision=%s\n", added.Alias, orNone(added.ResolvedRevision))
	return nil
}

func runCatalogRemove(cmd *cobra.Command, alias string) error {
	paths := mustPluginPaths()

	preview, err := service.PreviewCatalogRemove(paths, alias)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "removing catalog %q disables: %v\n", alias, preview.DisabledPlugins)

	switch {
	case catalogRemoveYes:
		fmt.Fprintln(cmd.OutOrStdout(), "removing via --yes")
	case isInteractive():
		if !confirm(cmd, "Remove this catalog and disable those plugins?") {
			return fmt.Errorf("aborted: catalog not removed")
		}
	default:
		return fmt.Errorf("catalog %q: re-run interactively to confirm, or pass --yes", alias)
	}

	removed, err := service.CommitCatalogRemove(paths, alias)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "removed %s: disabled %v\n", removed.Alias, removed.DisabledPlugins)
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "(none, path source)"
	}
	return s
}

// orNoneSubdir formats a catalog registration's --subdir for display: empty
// means the source root itself, not "no source", so it gets its own wording
// rather than orNone's revision-specific one.
func orNoneSubdir(s string) string {
	if s == "" {
		return "(none, source root)"
	}
	return s
}

// isInteractive reports whether stdin is a terminal — the CLI's own
// invocation environment, not something a cobra command's InOrStdin should
// virtualize, since the trust decision must reflect who is really typing.
var isInteractive = func() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// confirm prompts on cmd's output and reads a y/N answer from cmd's input.
func confirm(cmd *cobra.Command, prompt string) bool {
	return confirmReader(cmd, bufio.NewReader(cmd.InOrStdin()), prompt)
}

// confirmReader is confirm's core, taking an already-constructed reader so a
// multi-prompt flow (init) can share one bufio.Reader across every prompt —
// a fresh bufio.Reader per prompt on the same underlying stdin can silently
// drop bytes it had already buffered ahead for a later prompt.
func confirmReader(cmd *cobra.Command, reader *bufio.Reader, prompt string) bool {
	fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", prompt)
	line, _ := reader.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// promptLine prompts on cmd's output and reads one line of free-form text
// from reader, trimmed. See confirmReader for why the reader is shared
// across an entire multi-prompt flow rather than constructed per call.
func promptLine(cmd *cobra.Command, reader *bufio.Reader, prompt string) string {
	fmt.Fprint(cmd.OutOrStdout(), prompt)
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

func mustPluginPaths() service.PluginPaths {
	paths, err := service.DefaultPluginPaths()
	if err != nil {
		// Only a resolvable $HOME failure reaches here; every other
		// PluginPaths field is a pure path join. Panicking (rather than
		// plumbing a config-loading error through every catalog/plugin
		// command) matches how config.DefaultPath already treats an
		// unresolvable home directory as an unrecoverable environment
		// problem.
		panic(err)
	}
	return paths
}

func init() {
	catalogAddCmd.Flags().StringVar(&catalogAddRevision, "revision", "", "Git revision to resolve (required for git+https/git+ssh sources)")
	catalogAddCmd.Flags().StringVar(&catalogAddSubdir, "subdir", "", "Subdirectory of the source that becomes the catalog root (default: the source root itself)")
	catalogAddCmd.Flags().BoolVar(&catalogAddYes, "yes", false, "Register a catalog non-interactively (visible in command history)")

	catalogUpdateCmd.Flags().StringVar(&catalogUpdateRevision, "revision", "", "New git revision to resolve (required for a git-sourced catalog)")

	catalogRemoveCmd.Flags().BoolVar(&catalogRemoveYes, "yes", false, "Remove a catalog non-interactively (visible in command history)")

	catalogListCmd.Flags().BoolVar(&catalogListJSON, "json", false, "Output as JSON")

	catalogCmd.AddCommand(catalogAddCmd, catalogUpdateCmd, catalogRemoveCmd, catalogListCmd)
	rootCmd.AddCommand(catalogCmd)
}
