package config

import (
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// hookSource is one {{bin ...}}-eligible template string paired with the
// file it came from (for the bare-name plugin-local reading) and a
// human-readable label for error messages.
type hookSource struct {
	desc       string
	sourcePath string
	script     string
}

// checkBinRefs raises {{bin ...}} resolution from render time to load time:
// it statically scans every hook in hooks for `{{bin "ref"}}` calls and
// resolves each one against mounted, exactly as a real render would, but
// before any session ever runs the hook. registrations/lock/cacheRoot are
// consulted only to name a specific plugin to enable when a reference is
// otherwise unresolvable — see describeMissingPlugin.
//
// This only strengthens an already-load-failing config: a template this
// scan cannot parse (see plugins.BinRefs) is silently skipped here and
// still checked, as always, by the real renderer at render time.
func checkBinRefs(hooks []hookSource, mounted []plugins.Mounted, registrations *plugins.CatalogRegistrations, lock *plugins.Lockfile, cacheRoot string) error {
	for _, h := range hooks {
		if h.script == "" {
			continue
		}
		for _, ref := range plugins.BinRefs(h.script) {
			if _, err := plugins.ResolveBin(mounted, h.sourcePath, ref); err != nil {
				return fmt.Errorf("%s: %w%s", h.desc, err, describeMissingPlugin(ref, registrations, lock, cacheRoot))
			}
		}
	}
	return nil
}

// describeMissingPlugin returns a remediation suffix naming the plugin to
// enable when ref is the fully-qualified form and its catalog alias is
// registered: it re-resolves that catalog (local-only, the same call
// VerifyAndMountAll already makes, safe to repeat) and checks ref against
// every plugin path the catalog *publishes* — enabled or not, unlike
// mounted, which checkBinRefs already tried and failed against. Exactly one
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
