package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
)

// SessionProjectRoot is consulted before any upward search from dir, because
// a session's generated checkout usually carries its repository's committed
// marker: searching from inside one would offer the checkout itself as a root,
// written by whoever authored the checked-out branch. found reports that a
// live session claimed the invocation, in which case its recorded root is the
// only candidate and a root that lost its trust or marker is an error rather
// than a reason to search elsewhere.
func SessionProjectRoot(cfg *config.Config, store *state.Store, dir string) (root string, found bool, err error) {
	session, err := invokingSession(cfg, store, dir)
	if err != nil {
		return "", true, err
	}
	if session == nil {
		return "", false, nil
	}
	if err := cfg.RequireProjectRoot(session.ProjectRoot); err != nil {
		return "", true, err
	}
	return session.ProjectRoot, true, nil
}

// invokingSession prefers the ambient session over the cwd. A name that
// matches no live session is not an error: the variable can outlive its
// session in a shell that stays open.
func invokingSession(cfg *config.Config, store *state.Store, dir string) (*domain.Session, error) {
	if name := os.Getenv("PLECT_SESSION_NAME"); name != "" {
		s, err := store.GetE(name)
		if err != nil {
			return nil, fmt.Errorf("read session %q: %w", name, err)
		}
		if s != nil {
			return s, nil
		}
	}
	return sessionOwningDir(cfg, store, dir)
}

// sessionOwningDir skips a session whose workdir cannot be evaluated: its
// producer has not run yet, so no checkout of it exists to be inside.
func sessionOwningDir(cfg *config.Config, store *state.Store, dir string) (*domain.Session, error) {
	sessions, err := store.AllE()
	if err != nil {
		return nil, fmt.Errorf("read session state: %w", err)
	}
	dir = canonicalDir(dir)
	var deepest string
	var owners []*domain.Session
	for _, s := range sessions {
		wd, err := sessionWorkdir(cfg, s)
		if err != nil || wd == "" {
			continue
		}
		wd = canonicalDir(wd)
		if dir != wd && !strings.HasPrefix(dir, wd+string(filepath.Separator)) {
			continue
		}
		switch {
		case len(wd) > len(deepest):
			deepest, owners = wd, []*domain.Session{s}
		case wd == deepest:
			owners = append(owners, s)
		}
	}
	if len(owners) == 0 {
		return nil, nil
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i].Name < owners[j].Name })
	for _, s := range owners[1:] {
		if s.ProjectRoot != owners[0].ProjectRoot {
			names := make([]string, len(owners))
			for i, o := range owners {
				names[i] = o.Name
			}
			return nil, fmt.Errorf("sessions %s share the workdir %s but record different project roots; set PLECT_SESSION_NAME to the session to act for", strings.Join(names, ", "), deepest)
		}
	}
	return owners[0], nil
}

func canonicalDir(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}
