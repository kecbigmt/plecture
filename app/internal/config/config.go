package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/BurntSushi/toml"
)

// HookConfig represents a single hook command configuration.
type HookConfig struct {
	Command string `toml:"command"`
}

// HooksConfig holds all hook configurations by hook point.
type HooksConfig struct {
	PostSyncChange []HookConfig `toml:"post_sync_change"`
}

// Duration wraps time.Duration with a TOML UnmarshalText implementation so
// users can write strings like "7d" / "30m" in config.toml.
type Duration struct {
	time.Duration
}

// UnmarshalText parses a duration string. Accepts standard Go durations plus
// the "d" (day) suffix as a convenience (7d = 168h).
func (d *Duration) UnmarshalText(text []byte) error {
	s := string(text)
	if s == "" {
		d.Duration = 0
		return nil
	}
	// Translate trailing "d" (days) into hours since time.ParseDuration doesn't accept it.
	if n := len(s); n > 1 && s[n-1] == 'd' {
		head := s[:n-1]
		days, err := time.ParseDuration(head + "h")
		if err == nil {
			d.Duration = days * 24
			return nil
		}
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = dur
	return nil
}

// Task scope constants mirror contracts/state.TaskScope* but are duplicated
// here to avoid the config package depending on contracts/state.
const (
	TaskScopeSession = "session"
	TaskScopeRun     = "run"
)

// Execution plane values for TaskDefinition.Execution / ChannelDefinition.Execution
// / ResourceDef.Execution. "" (unset) means the context-specific default applies
// (see each field's doc comment).
const (
	ExecutionHost        = "host"
	ExecutionEnvironment = "environment"
)

type Config struct {
	WorktreesRoot string `toml:"worktrees_root"`
	// RepoAllowlist is the legacy GitHub-shaped allowlist (owner/repo exact
	// match). Superseded by ResourceAllowlist; kept as a compat alias —
	// entries are honored for inputs that look like GitHub URLs.
	RepoAllowlist []string `toml:"repo_allowlist"`
	// ResourceAllowlist is the security boundary for `tws create`: regex
	// patterns the resource identifier must match. The boundary exists
	// because agents (MCP) can invoke tws with arbitrary input, and a
	// session create executes trusted-layer shell against that input.
	// Only the user-owned global config can declare it. Empty (together
	// with RepoAllowlist) means allow all.
	ResourceAllowlist []string `toml:"resource_allowlist"`
	// PluginDirs lists plugin config roots (each containing workflows/ and
	// tasks/ subdirectories). Plugins form the base cascade layer: the
	// global layer and ancestor overlays stack on top of them. Entries are
	// trusted — only the user or the system's config-management tooling writes config.toml.
	PluginDirs       []string       `toml:"plugin_dirs"`
	Detached         bool           `toml:"detached"`
	Channels         []string       `toml:"channels"`
	Hooks            HooksConfig    `toml:"hooks"`
	InputsSchema     map[string]any `toml:"inputs_schema"`
	InputsSchemaFile string         `toml:"inputs_schema_file"`
	BaseDir          string         `toml:"-"`
	// SessionGuard is a per-session dispatch boundary sourced from the
	// TWS_SESSION_GUARD environment variable (not config.toml). When set, a
	// `tws create`/`up` may only produce a *resolved session name* that
	// matches this regex. The orchestrator's claude pane exports it from the
	// provider's `session_guard` output (e.g. "^acme/"), so a
	// prompt-injected board body cannot make the orchestrator dispatch work
	// outside its own session-name space. Empty = disabled.
	//
	// The guard is intentionally an opaque regex over the resolved session
	// name: tws core stays provider-agnostic (ADR-002) and never parses the
	// resource identifier's owner/URL structure — knowing that names are
	// owner-prefixed is the provider's job, encoded in the pattern it emits.
	SessionGuard string `toml:"-"`
}

// RepoConfig is the per-repo configuration read from <repoDir>/.tws/config.toml.
// Scalar fields (e.g. base_branch) are reserved for future use; only Hooks is
// currently consumed.
type RepoConfig struct {
	Hooks HooksConfig `toml:"hooks"`
}

func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		WorktreesRoot: filepath.Join(home, "worktrees"),
		RepoAllowlist: nil,
		Detached:      true,
		SessionGuard:  os.Getenv("TWS_SESSION_GUARD"),
	}
}

func Load() *Config {
	cfg := DefaultConfig()

	home, err := os.UserHomeDir()
	if err != nil {
		return cfg
	}

	configPath := filepath.Join(home, ".config", "tws", "config.toml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return cfg
	}

	if _, err := toml.DecodeFile(configPath, cfg); err != nil {
		return cfg
	}

	// Expand ~ in path configs
	if len(cfg.WorktreesRoot) > 0 && cfg.WorktreesRoot[0] == '~' {
		cfg.WorktreesRoot = filepath.Join(home, cfg.WorktreesRoot[1:])
	}
	for i, dir := range cfg.PluginDirs {
		if len(dir) > 0 && dir[0] == '~' {
			cfg.PluginDirs[i] = filepath.Join(home, dir[1:])
		}
	}

	cfg.BaseDir = configFileDir(configPath)

	return cfg
}

func (c *Config) ResolvedInputsSchemaPath() string {
	if c.InputsSchemaFile == "" {
		return ""
	}
	if filepath.IsAbs(c.InputsSchemaFile) {
		return c.InputsSchemaFile
	}
	if c.BaseDir == "" {
		return c.InputsSchemaFile
	}
	return filepath.Join(c.BaseDir, c.InputsSchemaFile)
}

// configFileDir resolves symlinks so sibling files (e.g. outputs_schema_file)
// are looked up next to the real config, not next to the symlink.
func configFileDir(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Dir(real)
	}
	return filepath.Dir(path)
}

// LoadRepoConfig reads per-repo config at <repoDir>/.tws/config.toml.
// Returns a zero RepoConfig if repoDir is empty or the file is missing/unreadable.
func LoadRepoConfig(repoDir string) RepoConfig {
	var rc RepoConfig
	if repoDir == "" {
		return rc
	}
	path := filepath.Join(repoDir, ".tws", "config.toml")
	if _, err := os.Stat(path); err != nil {
		return rc
	}
	if _, err := toml.DecodeFile(path, &rc); err != nil {
		return rc
	}
	return rc
}

// MergedHooks returns hooks for each hook point with global config hooks first
// followed by repo-specific overlay hooks. Both are executed in order.
func (c *Config) MergedHooks(repoDir string) HooksConfig {
	repo := LoadRepoConfig(repoDir)
	return HooksConfig{
		PostSyncChange: concatHooks(c.Hooks.PostSyncChange, repo.Hooks.PostSyncChange),
	}
}

func concatHooks(a, b []HookConfig) []HookConfig {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make([]HookConfig, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

// IsRepoAllowed checks if the given owner/repo is in the allowlist.
// If the allowlist is empty, all repos are allowed.
//
// Note the asymmetry with IsResourceAllowed: a non-empty ResourceAllowlist
// alone does not make this stricter — legacy callers that already hold an
// owner/repo keep their existing semantics.
func (c *Config) IsRepoAllowed(ownerRepo string) bool {
	if len(c.RepoAllowlist) == 0 {
		return true
	}
	return slices.Contains(c.RepoAllowlist, ownerRepo)
}

// IsSessionNameAllowed enforces the per-session SessionGuard boundary against
// a *resolved* session name. It is orthogonal to the allowlist: the allowlist
// is a static user-owned ceiling on resource identifiers, while SessionGuard
// is the dynamic per-orchestrator floor that keeps a session from dispatching
// work outside its own name space.
//
// Allow when SessionGuard is empty (disabled) or the regex matches the name.
// The guard runs on the resolver's canonical output — multiple input forms
// fold to one name, so there is no alternate-form bypass and no double parse
// of the resource identifier. An invalid pattern is an error (fail closed),
// mirroring IsResourceAllowed.
func (c *Config) IsSessionNameAllowed(sessionName string) (bool, error) {
	if c.SessionGuard == "" {
		return true, nil
	}
	re, err := regexp.Compile(c.SessionGuard)
	if err != nil {
		return false, fmt.Errorf("session guard %q: %w", c.SessionGuard, err)
	}
	return re.MatchString(sessionName), nil
}

// githubResourceRE extracts owner/repo from GitHub-shaped resource ids so
// legacy repo_allowlist entries keep gating the resolver path. Pure regex —
// no dependency on the github package.
var githubResourceRE = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)(/|$)`)

// IsResourceAllowed checks the resource identifier against the allowlist
// boundary before dispatch. Allow when:
//   - both allowlists are empty (boundary disabled), or
//   - any resource_allowlist regex matches, or
//   - the input is a GitHub URL whose owner/repo is in repo_allowlist
//     (compat with pre-resolver configs).
//
// Invalid patterns surface as errors — a silently-skipped pattern would
// fail open on exactly the inputs it was meant to block.
func (c *Config) IsResourceAllowed(resource string) (bool, error) {
	if len(c.ResourceAllowlist) == 0 && len(c.RepoAllowlist) == 0 {
		return true, nil
	}
	for _, pat := range c.ResourceAllowlist {
		re, err := regexp.Compile(pat)
		if err != nil {
			return false, fmt.Errorf("resource_allowlist pattern %q: %w", pat, err)
		}
		if re.MatchString(resource) {
			return true, nil
		}
	}
	if m := githubResourceRE.FindStringSubmatch(resource); m != nil {
		if len(c.RepoAllowlist) > 0 && slices.Contains(c.RepoAllowlist, m[1]+"/"+m[2]) {
			return true, nil
		}
	}
	return false, nil
}
