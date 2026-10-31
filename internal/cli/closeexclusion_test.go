package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/web"
)

// 02_NEW_368: a close follows the rule "Update now" follows.

func closingServer(t *testing.T, toolsDir string) *web.Server {
	t.Helper()
	t.Setenv(config.EnvToolsDir, toolsDir)
	token, err := web.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	server, err := web.NewServer(web.Options{Version: "1.0", Config: config.Config{}, Token: token, Paths: web.Paths{
		Profiles: filepath.Join(dir, "profiles"),
		Bindings: filepath.Join(dir, "bindings.json"),
		Builds:   filepath.Join(dir, "builds"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestACloseWaitsForABuildFromThisBundle(t *testing.T) {
	server := closingServer(t, t.TempDir())
	bundle := t.TempDir()
	self := managedtools.SelfInstall{Mode: managedtools.SelfPortable, Root: bundle}
	server.SetBundle(self)
	registry, err := web.BundleRegistry(self)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := registry.Use([]string{web.BundleTool}, "companion build run", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if exclusion, err := exclusionForClose(server, "1.900"); err == nil || exclusion != nil || !strings.Contains(err.Error(), "companion build run") {
		t.Fatalf("with a build running: %v %v", exclusion, err)
	}
	lease.Release()
	exclusion, err := exclusionForClose(server, "1.900")
	if err != nil {
		t.Fatalf("idle: %v", err)
	}
	if !exclusion.Held() {
		t.Fatal("the exclusion is not held")
	}
	exclusion.Release()
}

func TestACloseDoesNotReplaceOnAToolsFolderItCannotRead(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	server := closingServer(t, filepath.Join(file, "tools"))
	if exclusion, err := exclusionForClose(server, "1.900"); err == nil || exclusion != nil {
		t.Fatalf("nothing can be read and it goes ahead: %v %v", exclusion, err)
	}
}
