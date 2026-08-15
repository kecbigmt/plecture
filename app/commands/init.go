package commands

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
)

var (
	initCatalogAlias    string
	initCatalogSource   string
	initCatalogDir      string
	initCatalogRevision string
	initEnablePlugins   []string
	initAllowlist       []string
	initAllowAll        bool
	initWorkdirsRoot    string
	initYes             bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Bootstrap a fresh config home: register a catalog and enable plugins",
	Long: `Interactive first run for an empty config home (see "plect config show"
for which one is active): registers one plugin catalog through the same
trust-confirmation flow as "plect catalog add" (init never bypasses it),
enables whichever of its published plugin paths you select, and writes the
small set of genuinely per-user root config.toml values (resource
allowlist, workdirs root).

Refuses outright if this config home already has a config.toml or a
registered catalog, and never writes anything in that case — start from an
empty config home, or remove the existing one first.

Non-interactive scripted setups pass --yes together with every answer as an
explicit flag: --catalog-alias, --catalog-source, --enable (repeatable, at
least once), --allowlist (repeatable) or --allow-all, and --workdirs-root.
None of these fall back to a silent default outside an interactive
terminal: there is no default catalog source, no default "enable nothing",
no default "allow all", and no default workdirs root — every answer is
either typed at a prompt or spelled out on the command line.`,
	Args: cobra.NoArgs,
	RunE: runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	paths := mustPluginPaths()
	configPath := config.DefaultPath()
	if configPath == "" {
		return fmt.Errorf("resolve config home: unresolvable home directory")
	}

	done, err := service.InitAlreadyDone(configPath, paths)
	if err != nil {
		return err
	}
	if done {
		return fmt.Errorf("config home %s is already initialized; remove it, or point --config-home at an empty directory", filepath.Dir(configPath))
	}

	reader := bufio.NewReader(cmd.InOrStdin())

	alias, source, dir, revision, err := resolveInitCatalogAnswers(cmd, reader)
	if err != nil {
		return err
	}

	catalogParams := service.CatalogAddParams{Alias: alias, Source: source, Dir: dir, Revision: revision}
	preview, fetched, err := service.PreviewCatalogAdd(cmd.Context(), paths, catalogParams)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "catalog %q resolves to:\n  source:   %s\n  dir:      %s\n  revision: %s\n  plugins:  %v\n",
		preview.Alias, preview.Source, orNoneDir(dir), orNone(preview.ResolvedRevision), preview.Plugins)
	if preview.Description != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  description: %s\n", preview.Description)
	}

	enable, err := resolveInitPluginSelection(cmd, reader, preview.Plugins)
	if err != nil {
		return err
	}
	allowlist, err := resolveInitAllowlist(cmd, reader)
	if err != nil {
		return err
	}
	workdirsRoot, err := resolveInitWorkdirsRoot(cmd, reader)
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "will enable: %v\n", enable)
	fmt.Fprintf(cmd.OutOrStdout(), "resource allowlist: %v\n", orAllowAll(allowlist))
	fmt.Fprintf(cmd.OutOrStdout(), "workdirs root: %s\n", workdirsRoot)

	switch {
	case initYes:
		fmt.Fprintln(cmd.OutOrStdout(), "initializing via --yes")
	case isInteractive():
		if !confirmReader(cmd, reader, "Register this catalog and write config.toml?") {
			return fmt.Errorf("aborted: nothing written")
		}
	default:
		return fmt.Errorf("re-run interactively to confirm, or pass --yes")
	}

	if _, err := service.CommitCatalogAdd(paths, catalogParams, fetched); err != nil {
		return err
	}
	for _, path := range enable {
		if _, err := service.PluginAdd(cmd.Context(), paths, alias+"/"+path); err != nil {
			return fmt.Errorf("enable %s/%s: %w", alias, path, err)
		}
	}
	if err := service.WriteInitConfig(configPath, service.InitConfigValues{
		WorkdirsRoot:      workdirsRoot,
		ResourceAllowlist: allowlist,
	}); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "initialized %s\n", filepath.Dir(configPath))
	return nil
}

// resolveInitCatalogAnswers requires --catalog-alias/--catalog-source
// either way (interactively or as flags): there is no default catalog
// source to fall back on, so a scripted --yes run with neither flag set is
// a mistake, not an "add nothing" no-op.
func resolveInitCatalogAnswers(cmd *cobra.Command, reader *bufio.Reader) (alias, source, dir, revision string, err error) {
	alias = strings.TrimSpace(initCatalogAlias)
	source = strings.TrimSpace(initCatalogSource)
	dir = strings.TrimSpace(initCatalogDir)
	revision = strings.TrimSpace(initCatalogRevision)

	if !initYes && isInteractive() {
		if alias == "" {
			alias = promptLine(cmd, reader, "Catalog alias: ")
		}
		if source == "" {
			source = promptLine(cmd, reader, "Catalog source (git+https://, git+ssh://, path://, path+editable://): ")
		}
		if dir == "" {
			dir = promptLine(cmd, reader, "Catalog dir (subdirectory that is the catalog root; blank for the source root): ")
		}
		if revision == "" {
			revision = promptLine(cmd, reader, "Catalog revision (blank for a path source): ")
		}
	}
	if alias == "" || source == "" {
		return "", "", "", "", fmt.Errorf("--catalog-alias and --catalog-source are required (answer the prompts interactively, or pass both as explicit flags with --yes)")
	}
	return alias, source, dir, revision, nil
}

// resolveInitPluginSelection lets the user pick, by number or by path, from
// exactly what the just-registered catalog publishes — init has no
// provider-specific notion of "which plugins go together"; the catalog
// manifest is the only source of what's available.
func resolveInitPluginSelection(cmd *cobra.Command, reader *bufio.Reader, published []string) ([]string, error) {
	requested := initEnablePlugins
	switch {
	case !initYes && isInteractive():
		fmt.Fprintln(cmd.OutOrStdout(), "Published plugin paths:")
		for i, p := range published {
			fmt.Fprintf(cmd.OutOrStdout(), "  %d) %s\n", i+1, p)
		}
		requested = splitCommaList(promptLine(cmd, reader, "Enable which plugins? (comma-separated numbers or paths, blank for none): "))
	case len(requested) == 0:
		// A config home with no enabled plugins has no workflow to
		// dispatch: silently defaulting to "enable nothing" outside an
		// interactive terminal would leave init's promise ("a session can
		// be dispatched immediately afterward") unmet without ever telling
		// the caller.
		return nil, fmt.Errorf("--enable is required at least once (answer the prompt interactively, or pass --enable as an explicit flag with --yes)")
	}

	enable := make([]string, 0, len(requested))
	for _, token := range requested {
		path, err := resolvePluginToken(token, published)
		if err != nil {
			return nil, err
		}
		enable = append(enable, path)
	}
	return enable, nil
}

func resolvePluginToken(token string, published []string) (string, error) {
	if n, err := strconv.Atoi(token); err == nil {
		if n < 1 || n > len(published) {
			return "", fmt.Errorf("plugin selection %q is out of range (1-%d)", token, len(published))
		}
		return published[n-1], nil
	}
	if !stringSliceContains(published, token) {
		return "", fmt.Errorf("catalog does not publish plugin path %q", token)
	}
	return token, nil
}

func resolveInitAllowlist(cmd *cobra.Command, reader *bufio.Reader) ([]string, error) {
	patterns := initAllowlist
	switch {
	case !initYes && isInteractive():
		patterns = splitCommaList(promptLine(cmd, reader, "Resource allowlist patterns (regex, comma-separated; blank = allow all): "))
	case len(patterns) > 0 && initAllowAll:
		return nil, fmt.Errorf("--allowlist and --allow-all are mutually exclusive")
	case len(patterns) == 0 && !initAllowAll:
		// The allowlist is a security boundary checked on every session
		// create; outside an interactive terminal, "allow all" must be an
		// explicit, visible choice (--allow-all), not whatever happens
		// when nobody answers the question.
		return nil, fmt.Errorf("--allowlist (repeatable) or --allow-all is required (answer the prompt interactively, or pass one as an explicit flag with --yes)")
	}
	for _, p := range patterns {
		if _, err := regexp.Compile(p); err != nil {
			return nil, fmt.Errorf("resource allowlist pattern %q: %w", p, err)
		}
	}
	return patterns, nil
}

// resolveInitWorkdirsRoot's interactive suggestion mirrors
// config.DefaultConfig's own default (~/workdirs) rather than inventing a
// second default value, but only interactive Enter-to-accept counts as
// answering it: outside a terminal, --yes must say so explicitly, the same
// as every other init answer.
func resolveInitWorkdirsRoot(cmd *cobra.Command, reader *bufio.Reader) (string, error) {
	root := strings.TrimSpace(initWorkdirsRoot)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	defaultRoot := filepath.Join(home, "workdirs")

	switch {
	case !initYes && isInteractive():
		if answer := promptLine(cmd, reader, fmt.Sprintf("Workdirs root [%s]: ", defaultRoot)); answer != "" {
			root = answer
		} else {
			root = defaultRoot
		}
	case root == "":
		return "", fmt.Errorf("--workdirs-root is required (answer the prompt interactively, or pass an explicit flag with --yes)")
	}
	if len(root) > 0 && root[0] == '~' {
		root = filepath.Join(home, root[1:])
	}
	return root, nil
}

func splitCommaList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func stringSliceContains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func orAllowAll(patterns []string) string {
	if len(patterns) == 0 {
		return "(none — allow all)"
	}
	return fmt.Sprintf("%v", patterns)
}

func init() {
	initCmd.Flags().StringVar(&initCatalogAlias, "catalog-alias", "", "Local alias for the catalog to register (required with --yes)")
	initCmd.Flags().StringVar(&initCatalogSource, "catalog-source", "", "Catalog source: git+https://, git+ssh://, path://, or path+editable:// (required with --yes)")
	initCmd.Flags().StringVar(&initCatalogDir, "catalog-dir", "", "Subdirectory of the catalog source that becomes the catalog root (default: the source root itself)")
	initCmd.Flags().StringVar(&initCatalogRevision, "catalog-revision", "", "Git revision to resolve (required for a git-sourced catalog)")
	initCmd.Flags().StringArrayVar(&initEnablePlugins, "enable", nil, "Catalog-relative plugin path to enable (repeatable; required at least once with --yes)")
	initCmd.Flags().StringArrayVar(&initAllowlist, "allowlist", nil, "Resource identifier regex pattern to allow (repeatable; required with --yes unless --allow-all is passed)")
	initCmd.Flags().BoolVar(&initAllowAll, "allow-all", false, "Explicitly allow every resource identifier instead of passing --allowlist (mutually exclusive with it)")
	initCmd.Flags().StringVar(&initWorkdirsRoot, "workdirs-root", "", "Workdir root directory (required with --yes; interactive default ~/workdirs)")
	initCmd.Flags().BoolVar(&initYes, "yes", false, "Initialize non-interactively (visible in command history)")

	rootCmd.AddCommand(initCmd)
}
