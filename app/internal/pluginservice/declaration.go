package pluginservice

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/kecbigmt/plecture/app/internal/confighome"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

var serviceName = regexp.MustCompile("^[a-z][a-z0-9-]{0,63}$")
var envKey = regexp.MustCompile("^[A-Za-z_][A-Za-z0-9_]*$")

type Declaration struct {
	ID          string
	PluginID    string
	Name        string
	ExecPath    string
	Args        []string
	Env         map[string]string
	Config      map[string]any
	Restart     string
	HealthType  string
	ContentHash string
}
type operatorFile struct {
	Services []operatorService `toml:"services"`
}
type operatorService struct {
	Name       string         `toml:"name"`
	Executable string         `toml:"executable"`
	EnvFile    string         `toml:"env_file"`
	Args       []string       `toml:"args"`
	Restart    string         `toml:"restart"`
	Health     serviceHealth  `toml:"health"`
	Config     map[string]any `toml:"config"`
}
type serviceHealth struct {
	Type string `toml:"type"`
}

func BuildDeclarations(mounted []plugins.Mounted, lock *plugins.Lockfile) ([]Declaration, error) {
	home, err := confighome.Resolve()
	if err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(home, "services", "*.toml"))
	if err != nil {
		return nil, err
	}
	executables := make(map[string]struct {
		mounted    plugins.Mounted
		executable plugins.Executable
	})
	for _, m := range mounted {
		for _, ex := range m.Manifest.Executables {
			executables[m.ID+"/"+ex.Name] = struct {
				mounted    plugins.Mounted
				executable plugins.Executable
			}{m, ex}
		}
	}
	var out []Declaration
	names := map[string]bool{}
	bindings := map[string]bool{}
	for _, path := range files {
		var file operatorFile
		meta, err := toml.DecodeFile(path, &file)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if keys := meta.Undecoded(); len(keys) > 0 {
			return nil, fmt.Errorf("%s: unknown key %s", path, keys[0])
		}
		for _, svc := range file.Services {
			if !serviceName.MatchString(svc.Name) {
				return nil, fmt.Errorf("%s: invalid service name %q", path, svc.Name)
			}
			if names[svc.Name] {
				return nil, fmt.Errorf("%s: duplicate service name %q", path, svc.Name)
			}
			names[svc.Name] = true
			if bindings[svc.Executable] {
				return nil, fmt.Errorf("%s: duplicate executable %q", path, svc.Executable)
			}
			bindings[svc.Executable] = true
			resolved, ok := executables[svc.Executable]
			if !ok {
				return nil, fmt.Errorf("%s: service %q: executable %q is not mounted", path, svc.Name, svc.Executable)
			}
			if !resolved.executable.Service {
				return nil, fmt.Errorf("%s: service %q: executable %q is not marked service = true", path, svc.Name, svc.Executable)
			}
			if svc.Restart == "" {
				svc.Restart = plugins.ServiceRestartOnFailure
			}
			if svc.Restart != plugins.ServiceRestartOnFailure && svc.Restart != plugins.ServiceRestartNever {
				return nil, fmt.Errorf("%s: service %q: invalid restart %q", path, svc.Name, svc.Restart)
			}
			if svc.Health.Type == "" {
				svc.Health.Type = plugins.ServiceHealthProcess
			}
			if svc.Health.Type != plugins.ServiceHealthProcess {
				return nil, fmt.Errorf("%s: service %q: invalid health type %q", path, svc.Name, svc.Health.Type)
			}
			env, err := readEnvFile(svc.EnvFile)
			if err != nil {
				return nil, fmt.Errorf("%s: service %q: env_file: %w", path, svc.Name, err)
			}
			if svc.Config == nil {
				svc.Config = map[string]any{}
			}
			if resolved.executable.ConfigSchema == nil {
				return nil, fmt.Errorf("%s: service %q: executable %q has no config_schema", path, svc.Name, svc.Executable)
			}
			schema, err := lang.CompileInlineSchema(resolved.executable.ConfigSchema, "service-config.json")
			if err != nil {
				return nil, fmt.Errorf("%s: service %q: config_schema: %w", path, svc.Name, err)
			}
			if err := schema.Validate(svc.Config); err != nil {
				return nil, fmt.Errorf("%s: service %q: config: %w", path, svc.Name, err)
			}
			hash := ""
			if lock != nil {
				if entry, ok := lock.FindPlugin(resolved.mounted.ID); ok {
					hash = entry.ContentHash
				}
			}
			out = append(out, Declaration{ID: svc.Name, Name: svc.Name, PluginID: resolved.mounted.ID, ExecPath: filepath.Join(resolved.mounted.Dir, resolved.executable.Path), Args: svc.Args, Env: env, Config: svc.Config, Restart: svc.Restart, HealthType: svc.Health.Type, ContentHash: hash})
		}
	}
	return out, nil
}

func readEnvFile(path string) (map[string]string, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Mode().Perm()&0o400 == 0 {
		return nil, fmt.Errorf("file must be owner-readable and private")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	env := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !envKey.MatchString(key) || strings.Contains(value, "${") || strings.Contains(value, "$(") {
			return nil, fmt.Errorf("invalid entry %q", line)
		}
		if strings.HasPrefix(key, "PLECT_") {
			return nil, fmt.Errorf("%s conflicts with plect runtime", key)
		}
		if _, exists := env[key]; exists {
			return nil, fmt.Errorf("duplicate key %s", key)
		}
		env[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return env, nil
}
