package config

import (
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// describeMissingPlugin returns a remediation suffix naming the plugin to
// enable when ref is the fully-qualified form and its catalog alias is
// registered: it re-resolves that catalog (local-only, the same call
// VerifyAndMountAll already makes, safe to repeat) and checks ref against
// every plugin path the catalog *publishes* — enabled or not, unlike
// mounted, which the caller already tried and failed against. Exactly one
// matching published path names the plugin unambiguously; zero or more than
// one leaves the caller's original error to stand on its own rather than
// guess.
func describeMissingPlugin(ref string, registrations *plugins.CatalogRegistrations, lock *plugins.Lockfile, cacheRoot string) string {
	if registrations == nil {
		return ""
	}
	alias, rest, ok := strings.Cut(ref, "/")
	if !ok || rest == "" {
		return ""
	}
	for _, entry := range registrations.Catalogs {
		if entry.Alias != alias {
			continue
		}
		rc, err := plugins.VerifyAndMountCatalog(entry, lock, cacheRoot)
		if err != nil {
			return ""
		}
		match, ok := singlePublishedPluginMatch(rest, rc.Manifest.Plugins)
		if !ok {
			return ""
		}
		id := alias + "/" + match
		return fmt.Sprintf("; plugin %q is not enabled — run `plect plugin add %s`", id, id)
	}
	return ""
}

// singlePublishedPluginMatch finds the one published path that is either
// equal to rest or a "/"-bounded prefix of it (the same two readings
// plugins.ResolveBin tries against mounted plugin ids). Multiple matches are
// reported as no match: nested plugin paths can collide the same way
// ResolveBin itself refuses to guess between, and a hint that is sometimes
// wrong is worse than no hint.
func singlePublishedPluginMatch(rest string, published []string) (string, bool) {
	match := ""
	found := false
	for _, p := range published {
		if p != rest && !strings.HasPrefix(rest, p+"/") {
			continue
		}
		if found {
			return "", false
		}
		match = p
		found = true
	}
	return match, found
}

// MountedBins resolves an action's `bin` reference against the plugins
// mounted on this machine, returning the executable's path — the same
// resolution a rendered `{{bin ...}}` performed, raised to the language's
// own reference contract. SourcePath is the file the reference was written
// in, which is what identifies the containing plugin a bare name resolves
// against.
type MountedBins struct {
	Mounted    []plugins.Mounted
	SourcePath string
	// registrations/lock/cacheRoot are only what describeMissingPlugin needs
	// to name a plugin to enable. They are unexported because that hint
	// belongs to a load, where the whole catalog state is at hand; a runtime
	// resolution of an already-validated reference gets no hint and needs
	// none.
	registrations *plugins.CatalogRegistrations
	lock          *plugins.Lockfile
	cacheRoot     string
}

// binResolver resolves the bin references in one definition document.
func (c *Config) binResolver(sourcePath string) MountedBins {
	return MountedBins{
		Mounted:       c.Plugins,
		SourcePath:    sourcePath,
		registrations: c.catalogRegistrations,
		lock:          c.catalogLock,
		cacheRoot:     c.catalogCacheRoot,
	}
}

// ResolveBin resolves ref from the layer that wrote it. Ownership adds the
// one rule plugins.ResolveBin does not enforce: shipped plugin config never
// names another plugin's executable, because it cannot know the alias the
// user registered that plugin's catalog under.
func (m MountedBins) ResolveBin(ref string, from lang.Ownership) (string, error) {
	if from.IsPlugin && strings.Contains(ref, "/") {
		return "", fmt.Errorf("shipped plugin config cannot reference another plugin's executable; %q is not a bare name", ref)
	}
	path, err := plugins.ResolveBin(m.Mounted, m.SourcePath, ref)
	if err != nil {
		return "", fmt.Errorf("%w%s", err, describeMissingPlugin(ref, m.registrations, m.lock, m.cacheRoot))
	}
	return path, nil
}
