// Package pluginservice supervises plugin-owned daemons declared by
// [[services]] in plugin.toml.
package pluginservice

import (
	"fmt"
	"path/filepath"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// Declaration is one resolved [[services]] entry, ready to run: ExecPath is
// already resolved to an absolute path inside the mounted plugin directory,
// and ContentHash is the plugin's lock content hash (empty for a plugin with
// no lock entry, e.g. a non-reproducible editable-path catalog) used to
// detect a content change that should restart the service.
type Declaration struct {
	ID          string
	PluginID    string
	Name        string
	ExecPath    string
	Args        []string
	Env         map[string]string
	RequiredEnv []string
	Restart     string
	HealthType  string
	ContentHash string
}

// BuildDeclarations resolves every [[services]] entry across mounted plugins
// into a ready-to-run Declaration. plugins.LoadManifest already guarantees
// each service's `executable` names one of the same plugin's own declared
// executables, so the lookup here only fails for a Manifest built by
// something other than LoadManifest (see BuildDeclarations' own test for
// that defensive case).
func BuildDeclarations(mounted []plugins.Mounted, lock *plugins.Lockfile) ([]Declaration, error) {
	var out []Declaration
	for _, m := range mounted {
		hash := ""
		if lock != nil {
			if entry, ok := lock.FindPlugin(m.ID); ok {
				hash = entry.ContentHash
			}
		}
		for _, svc := range m.Manifest.Services {
			execPath := ""
			for _, ex := range m.Manifest.Executables {
				if ex.Name == svc.Executable {
					execPath = filepath.Join(m.Dir, ex.Path)
					break
				}
			}
			if execPath == "" {
				return nil, fmt.Errorf("plugin %q: service %q: executable %q is not declared by this plugin", m.ID, svc.Name, svc.Executable)
			}
			out = append(out, Declaration{
				ID:          m.ID + "/" + svc.Name,
				PluginID:    m.ID,
				Name:        svc.Name,
				ExecPath:    execPath,
				Args:        svc.Args,
				Env:         svc.Env,
				RequiredEnv: svc.RequiredEnv,
				Restart:     svc.Restart,
				HealthType:  svc.Health.Type,
				ContentHash: hash,
			})
		}
	}
	return out, nil
}
