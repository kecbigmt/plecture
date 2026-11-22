package ghapi

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAppFromEnv_UnsetReturnsNilClient(t *testing.T) {
	client, err := AppFromEnv(filepath.Join(t.TempDir(), "cache.json"))
	if err != nil {
		t.Fatalf("AppFromEnv: %v", err)
	}
	if client != nil {
		t.Fatalf("client = %+v, want nil when %s is unset", client, EnvAppID)
	}
}

func TestAppFromEnv_TokenCommandIsExclusive(t *testing.T) {
	for _, tc := range []struct {
		name, key, command string
		valid              bool
	}{
		{"command", "", `["/host/token","get"]`, true},
		{"both", "/private/key", `["/host/token"]`, false},
		{"neither", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvAppID, "123456")
			t.Setenv(EnvInstallationID, "789012")
			t.Setenv(EnvPrivateKeyPath, tc.key)
			t.Setenv(EnvTokenCommand, tc.command)
			_, err := AppFromEnv(filepath.Join(t.TempDir(), "cache.json"))
			if (err == nil) != tc.valid {
				t.Fatalf("error = %v", err)
			}
			if err != nil && (!strings.Contains(err.Error(), EnvPrivateKeyPath) || !strings.Contains(err.Error(), EnvTokenCommand)) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestAppFromEnv_CommandNeedsNoAppIdentifiers(t *testing.T) {
	t.Setenv(EnvAppID, "")
	t.Setenv(EnvInstallationID, "")
	t.Setenv(EnvOwner, "")
	t.Setenv(EnvRepo, "")
	t.Setenv(EnvTokenCommand, `["/host/token"]`)
	app, err := AppFromEnv(t.TempDir())
	if err != nil || app == nil {
		t.Fatalf("app = %v, error = %v", app, err)
	}
}

func TestAppFromEnv_AppIDWithoutPrivateKeyPathFailsLoud(t *testing.T) {
	t.Setenv(EnvAppID, "123456")
	t.Setenv(EnvInstallationID, "789012")
	if _, err := AppFromEnv(filepath.Join(t.TempDir(), "cache.json")); err == nil {
		t.Fatal("want an error when the App id is set but the private key path is not")
	}
}

func TestAppFromEnv_AppIDWithoutInstallationOrOwnerRepoFailsLoud(t *testing.T) {
	t.Setenv(EnvAppID, "123456")
	t.Setenv(EnvPrivateKeyPath, writeTestAppKey(t))
	if _, err := AppFromEnv(filepath.Join(t.TempDir(), "cache.json")); err == nil {
		t.Fatal("want an error when neither installation id nor owner+repo identify an installation")
	}
}

func TestAppFromEnv_ValidConfigBuildsClient(t *testing.T) {
	t.Setenv(EnvAppID, "123456")
	t.Setenv(EnvInstallationID, "789012")
	t.Setenv(EnvPrivateKeyPath, writeTestAppKey(t))
	client, err := AppFromEnv(filepath.Join(t.TempDir(), "cache.json"))
	if err != nil {
		t.Fatalf("AppFromEnv: %v", err)
	}
	if client == nil || client.AppID != "123456" || client.InstallationID != "789012" {
		t.Fatalf("client = %+v", client)
	}
}

func TestAppFromEnv_CachePathDefaultsToArgumentWhenEnvUnset(t *testing.T) {
	t.Setenv(EnvAppID, "123456")
	t.Setenv(EnvInstallationID, "789012")
	t.Setenv(EnvPrivateKeyPath, writeTestAppKey(t))
	want := filepath.Join(t.TempDir(), "shared-cache.json")
	client, err := AppFromEnv(want)
	if err != nil {
		t.Fatalf("AppFromEnv: %v", err)
	}
	if client.CachePath != want {
		t.Errorf("CachePath = %q, want %q", client.CachePath, want)
	}
}

func TestAppFromEnv_EnvCachePathOverridesArgument(t *testing.T) {
	t.Setenv(EnvAppID, "123456")
	t.Setenv(EnvInstallationID, "789012")
	t.Setenv(EnvPrivateKeyPath, writeTestAppKey(t))
	override := filepath.Join(t.TempDir(), "override-cache.json")
	t.Setenv(EnvCachePath, override)
	client, err := AppFromEnv(filepath.Join(t.TempDir(), "default-cache.json"))
	if err != nil {
		t.Fatalf("AppFromEnv: %v", err)
	}
	if client.CachePath != override {
		t.Errorf("CachePath = %q, want %q", client.CachePath, override)
	}
}
