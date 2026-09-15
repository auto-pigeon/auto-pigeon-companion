package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOnlyTheTwoPublicDeploymentsAreOfficial(t *testing.T) {
	for address, want := range map[string]bool{
		"https://auto-pigeon.com": true, "https://beta.auto-pigeon.com/": true,
		"http://auto-pigeon.com": false, "https://aub.example": false, "": false,
	} {
		if got := IsOfficialBackend(address); got != want {
			t.Errorf("IsOfficialBackend(%q) = %v", address, got)
		}
	}
	for _, backend := range OfficialBackends {
		if backend.URL == "" || filepath.IsAbs(backend.URL) {
			t.Errorf("backend %+v", backend)
		}
	}
}

// TestNothingIsChosenByDefault is the HITL half that keeps a first run
// network-free: offering two deployments is not choosing one.
func TestNothingIsChosenByDefault(t *testing.T) {
	t.Setenv(EnvAUBBaseURL, "")
	os.Unsetenv(EnvAUBBaseURL)
	if address, err := Default().AUB(); err == nil {
		t.Errorf("a first run resolved an address: %q", address)
	}
}

func TestTheOverrideBesideTheExecutableSetsTheAddressAndPort(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, OverrideFileName),
		[]byte(`{"aub_base_url":"http://localhost:9190","port":8800}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := fakeEnv{}
	report, err := LoadExecutableOverride(filepath.Join(dir, "companion"), env.lookup, env.set)
	if err != nil {
		t.Fatal(err)
	}
	if env[EnvAUBBaseURL] != "http://localhost:9190" || env[EnvPort] != "8800" || len(report.Applied) != 2 {
		t.Errorf("env = %v, report = %+v", env, report)
	}
}

func TestTheOverrideRefusesAKeyItCannotHonour(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, OverrideFileName),
		[]byte(`{"catalog_anchors_path":"/tmp/evil"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := fakeEnv{}
	if _, err := LoadExecutableOverride(filepath.Join(dir, "companion"), env.lookup, env.set); err == nil {
		t.Error("an override naming a trust anchor was accepted")
	}
	if len(env) != 0 {
		t.Errorf("a refused override set %v", env)
	}
}

func TestNoOverrideFileChangesNothing(t *testing.T) {
	env := fakeEnv{}
	report, err := LoadExecutableOverride(filepath.Join(t.TempDir(), "companion"), env.lookup, env.set)
	if err != nil || report.Path != "" || len(env) != 0 {
		t.Errorf("report = %+v, err = %v, env = %v", report, err, env)
	}
}
