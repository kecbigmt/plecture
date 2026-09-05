package ghapi

import (
	"path/filepath"
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
