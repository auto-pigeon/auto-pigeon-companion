package web

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/selfupdate"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/tooluse"
)

// 02_NEW_368: no bundle is replaced while a build runs, whichever way the
// other process shares it. The other process is a real one (leaseHelperMain).

// selfServer is a server with its own tools folder that runs from its own
// portable bundle folder.
func selfServer(t *testing.T) (*Server, string) {
	t.Helper()
	return selfServerWith(t, t.TempDir())
}

func selfServerWith(t *testing.T, tools string) (*Server, string) {
	t.Helper()
	t.Setenv(config.EnvToolsDir, tools)
	server, _ := newTestServer(t, nil)
	server.lifecycle = NewLifecycle(LifecycleOptions{Interactive: true})
	bundle := t.TempDir()
	server.SetBundle(managedtools.SelfInstall{Mode: managedtools.SelfPortable, Root: bundle})
	if server.bundleUseErr != nil {
		t.Fatal(server.bundleUseErr)
	}
	return server, bundle
}

func bundleRoot(root string) string { return filepath.Join(root, selfupdate.DirName) }

func restart(t *testing.T, server *Server) []string {
	t.Helper()
	busy, err := selfHost{s: server}.RestartForUpdate(managedtools.InstallRecord{Version: "1.900"})
	if err != nil {
		t.Fatal(err)
	}
	return busy
}

func stopped(server *Server) bool {
	_, stopped := server.SelfRestartRecord()
	return stopped
}

// The same bundle with another tools folder: nothing in the tools folders
// connects the two processes; the bundle's own folder does.
func TestABuildFromThisBundleWithOtherToolsHoldsTheUpdate(t *testing.T) {
	server, bundle := selfServer(t)
	_, stdin := otherProcess(t, bundleRoot(bundle), BundleTool)
	if busy := restart(t, server); len(busy) == 0 || !strings.Contains(busy[0], "test process") || stopped(server) {
		t.Fatalf("busy %v, stopped %v", busy, stopped(server))
	}
	if server.admission.isClosed() {
		t.Fatal("the fence stayed closed while the update waits")
	}
	stdin.Close()
	waitUntilIdle(t, server)
	if !stopped(server) {
		t.Fatal("the update did not proceed once the build ended")
	}
}

// Another bundle in this tools folder: the conservative rule of the user.
func TestABuildFromAnotherBundleInTheseToolsHoldsTheUpdate(t *testing.T) {
	server, _ := selfServer(t)
	registry, err := server.ToolRegistry()
	if err != nil {
		t.Fatal(err)
	}
	_, stdin := otherProcess(t, registry.Root(), BundleTool, string(managedtools.EricW))
	if busy := restart(t, server); len(busy) == 0 || stopped(server) {
		t.Fatalf("busy %v, stopped %v", busy, stopped(server))
	}
	stdin.Close()
	waitUntilIdle(t, server)
}

// Nothing shared, nothing to wait for.
func TestABuildSharingNothingHoldsNoUpdate(t *testing.T) {
	server, _ := selfServer(t)
	otherProcess(t, bundleRoot(t.TempDir()), BundleTool)
	otherProcess(t, t.TempDir(), BundleTool, string(managedtools.EricW))
	if busy := restart(t, server); len(busy) != 0 || !stopped(server) {
		t.Fatalf("busy %v, stopped %v", busy, stopped(server))
	}
	server.HeldSelfExclusion().Release()
}

// Two spellings of one folder are one folder.
func TestAnotherSpellingOfTheBundleFolderIsTheSameBundle(t *testing.T) {
	server, bundle := selfServer(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(bundle, alias); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	_, stdin := otherProcess(t, bundleRoot(alias), BundleTool)
	if busy := restart(t, server); len(busy) == 0 || stopped(server) {
		t.Fatalf("busy %v, stopped %v", busy, stopped(server))
	}
	stdin.Close()
	waitUntilIdle(t, server)
}

// A pipeline holds the bundle and the tools folder; held gates refuse a new
// one retryably, in either folder, and refusing it leaves no lease behind.
func TestAPipelineRacingTheReplacementIsRefusedAndKeepsNothing(t *testing.T) {
	server, bundle := selfServer(t)
	if busy := restart(t, server); len(busy) != 0 {
		t.Fatalf("busy %v", busy)
	}
	exclusion := server.HeldSelfExclusion()
	defer exclusion.Release()
	registry, _ := server.ToolRegistry()
	_, err := ToolLeaser(registry, server.bundleUse, "racing build", 0)(nil)
	if !errors.Is(err, tooluse.ErrSwitching) || !isRetryLater(err) {
		t.Fatalf("a pipeline racing the gate: %v", err)
	}
	if !errors.Is(server.toolsSwitching(""), tooluse.ErrSwitching) {
		t.Fatal("the pre-check does not see the switch")
	}
	own := filepath.Join(bundle, selfupdate.DirName, tooluse.DirName)
	entries, err := os.ReadDir(own)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "use-") {
			t.Fatalf("a refused pipeline left %s", entry.Name())
		}
	}
	exclusion.Release()
	lease, err := ToolLeaser(registry, server.bundleUse, "next build", 0)(nil)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

// A custom compiler uses the bundle as much as a managed one does.
func TestEveryPipelineHoldsTheBundle(t *testing.T) {
	if tools := ToolsFor([]string{"local.compiler.mine"}); len(tools) != 1 || tools[0] != BundleTool {
		t.Fatalf("a custom compiler's pipeline holds %v", tools)
	}
}

// A tools folder that cannot be used is not an idle one.
func TestAnUnusableToolsFolderLeavesTheUpdateWaiting(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	server, _ := selfServerWith(t, filepath.Join(file, "tools"))
	if busy := restart(t, server); len(busy) == 0 || !strings.Contains(busy[0], "cannot be used") || stopped(server) {
		t.Fatalf("busy %v, stopped %v", busy, stopped(server))
	}
	if server.admission.isClosed() {
		t.Fatal("the fence stayed closed")
	}
}

// A bundle folder that cannot be used is not an idle one, and runs no build.
func TestAnUnusableBundleFolderLeavesTheUpdateWaiting(t *testing.T) {
	server, _ := selfServer(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	server.SetBundle(managedtools.SelfInstall{Mode: managedtools.SelfPortable, Root: file})
	if server.bundleUseErr == nil {
		t.Fatal("a file was accepted as a bundle folder")
	}
	if busy := restart(t, server); len(busy) == 0 || !strings.Contains(busy[0], "cannot be used") || stopped(server) {
		t.Fatalf("busy %v, stopped %v", busy, stopped(server))
	}
}

// A folder a Companion does not replace has no coordination of its own.
func TestOnlyAPortableBundleHasARegistry(t *testing.T) {
	for _, mode := range []managedtools.SelfMode{managedtools.SelfManual, managedtools.SelfDevelopment} {
		root := t.TempDir()
		registry, err := BundleRegistry(managedtools.SelfInstall{Mode: mode, Root: root})
		if registry != nil || err != nil {
			t.Fatalf("%s: %v %v", mode, registry, err)
		}
		if entries, _ := os.ReadDir(root); len(entries) != 0 {
			t.Fatalf("%s: wrote %v", mode, entries)
		}
	}
}

// waitUntilIdle retries as the coordinator's own step does.
func waitUntilIdle(t *testing.T, server *Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(restart(t, server)) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the update still waits after the build ended")
		}
		time.Sleep(20 * time.Millisecond)
	}
	server.HeldSelfExclusion().Release()
}

// Refused by the bundle after the tools folder admitted it: the lease in the
// tools folder is given back, or a compiler update would wait for nothing.
func TestAPipelineRefusedByTheBundleGivesBackItsToolsLease(t *testing.T) {
	server, _ := selfServer(t)
	gate, err := server.bundleUse.Switch(BundleTool, "the Companion replacing itself")
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	registry, err := server.ToolRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ToolLeaser(registry, server.bundleUse, "racing build", 0)(nil); !errors.Is(err, tooluse.ErrSwitching) {
		t.Fatalf("a pipeline racing the bundle's gate: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(registry.Root(), tooluse.DirName))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "use-") {
			t.Fatalf("a refused pipeline left %s", entry.Name())
		}
	}
}
