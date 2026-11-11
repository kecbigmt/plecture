package effect

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kecbigmt/plecture/app/internal/cachehome"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/pluginservice"
)

func pluginEnvironment(execPath string, mounted []plugins.Mounted) ([]string, error) {
	runtime, err := PluginRuntime(execPath, mounted)
	if err != nil {
		return nil, err
	}
	env := runtime.Env
	if runtime.Prepare != nil {
		serviceEnv, err := runtime.Prepare()
		if err != nil {
			return nil, err
		}
		env = append(env, serviceEnv...)
	}
	return env, nil
}

func PluginRuntime(execPath string, mounted []plugins.Mounted) (lang.BinRuntime, error) {
	for _, m := range mounted {
		for _, executable := range m.Manifest.Executables {
			if filepath.Join(m.Dir, executable.Path) != execPath {
				continue
			}
			root, err := cachehome.Resolve()
			if err != nil {
				return lang.BinRuntime{}, err
			}
			cacheDir := filepath.Join(root, "plugins", m.ID)
			if err := os.MkdirAll(cacheDir, 0o700); err != nil {
				return lang.BinRuntime{}, fmt.Errorf("create plugin cache: %w", err)
			}
			if err := os.Chmod(cacheDir, 0o700); err != nil {
				return lang.BinRuntime{}, err
			}
			env := []string{"PLECT_CACHE_DIR=" + cacheDir}
			if !executable.Service {
				return lang.BinRuntime{Env: env}, nil
			}
			id := m.ID + "/" + executable.Name
			declarations, err := pluginservice.BuildDeclarations(mounted, nil)
			if err := pluginservice.ExecutableError(err, id); err != nil {
				return lang.BinRuntime{}, err
			}
			prepare := func() ([]string, error) {
				current, err := pluginservice.BuildDeclarations(mounted, nil)
				if err := pluginservice.ExecutableError(err, id); err != nil {
					return nil, err
				}
				for _, decl := range current {
					if decl.PluginID == m.ID && decl.ExecPath == execPath {
						out := make([]string, 0, len(decl.Env))
						for key, value := range decl.Env {
							out = append(out, key+"="+value)
						}
						return out, nil
					}
				}
				return nil, fmt.Errorf("service executable %q has no operator declaration", m.ID+"/"+executable.Name)
			}
			for _, decl := range declarations {
				if decl.PluginID == m.ID && decl.ExecPath == execPath {
					return lang.BinRuntime{Env: env, Config: decl.Config, Prepare: prepare}, nil
				}
			}
			return lang.BinRuntime{Env: env}, nil
		}
	}
	return lang.BinRuntime{}, nil
}
