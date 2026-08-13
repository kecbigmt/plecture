package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/BurntSushi/toml"
)

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
	WorkdirsRoot string `toml:"workdirs_root"`
	// ResourceAllowlist is the security boundary for session creation: regex
	// patterns the resource identifier must match. The boundary exists
	// because agents (MCP) can invoke plect with arbitrary input, and a
	// session create executes trusted-layer shell against that input.
	// Only the user-owned global config can declare it. Empty means allow
	// all.
	ResourceAllowlist []string `toml:"resource_allowlist"`
	// PluginDirs lists plugin config roots (each containing workflows/ and
	// tasks/ subdirectories). Plugins form the base cascade layer: the
	// global layer and ancestor overlays stack on top of them. Entries are
	// trusted — only the user or the system's config-management tooling writes config.toml.
	PluginDirs       []string       `toml:"plugin_dirs"`
	Detached         bool           `toml:"detached"`
	Channels         []string       `toml:"channels"`
	InputsSchema     map[string]any `toml:"inputs_schema"`
	InputsSchemaFile string         `toml:"inputs_schema_file"`
	BaseDir          string         `toml:"-"`
	// SessionGuard is a per-session dispatch boundary sourced from the
	// PLECT_SESSION_GUARD environment variable (not config.toml). When set, a
	// `plect up` may only produce a *resolved session name* that
	// matches this regex. The orchestrator's claude pane exports it from the
	// provider's `session_guard` output (e.g. "^acme/"), so a
	// prompt-injected board body cannot make the orchestrator dispatch work
	// outside its own session-name space. Empty = disabled.
	//
	// The guard is intentionally an opaque regex over the resolved session
	// name: plect core stays provider-agnostic and never parses the
	// resource identifier's internal structure — knowing how names are
	// shaped is the provider's job, encoded in the pattern it emits.
	SessionGuard string `toml:"-"`
}

func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		WorkdirsRoot: filepath.Join(home, "workdirs"),
		Detached:     true,
		SessionGuard: os.Getenv("PLECT_SESSION_GUARD"),
	}
}

// DefaultPath returns the path Load reads config.toml from. Callers outside
// the normal load path (e.g. the migration command, which must locate the
// same file Load would) use this instead of duplicating the join.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "plect", "config.toml")
}

func Load() *Config {
	cfg := DefaultConfig()

	configPath := DefaultPath()
	if configPath == "" {
		return cfg
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return cfg
	}

	meta, err := toml.DecodeFile(configPath, cfg)
	if err != nil {
		// A present but unparsable config.toml is a user mistake, not an
		// absent-config no-op: silently falling back to defaults here would
		// hide a typo behind seemingly-ignored settings, so this warns
		// (disposition: surfaced) while still returning usable defaults
		// (Load has no error return in its signature, so a hard failure
		// would require a wider API change out of scope for this fix).
		slog.Warn("config.toml present but failed to parse; using defaults", "path", configPath, "error", err)
		return cfg
	}
	if meta.IsDefined("worktrees_root") {
		slog.Warn("legacy config key worktrees_root is ignored; rename it to workdirs_root or run the legacy migration", "path", configPath)
	}

	// Expand ~ in path configs
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		return cfg
	}
	if len(cfg.WorkdirsRoot) > 0 && cfg.WorkdirsRoot[0] == '~' {
		cfg.WorkdirsRoot = filepath.Join(home, cfg.WorkdirsRoot[1:])
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

// IsResourceAllowed checks the resource identifier against the allowlist
// boundary before dispatch. Allow when the allowlist is empty (boundary
// disabled) or any resource_allowlist regex matches.
//
// Invalid patterns surface as errors — a silently-skipped pattern would
// fail open on exactly the inputs it was meant to block.
func (c *Config) IsResourceAllowed(resource string) (bool, error) {
	if len(c.ResourceAllowlist) == 0 {
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
	return false, nil
}
