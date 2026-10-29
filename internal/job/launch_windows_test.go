//go:build windows

package job

import (
	"os/exec"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/noconsole"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// The flags a job's process is actually started with: a tool gets its own
// process group AND no console window; a game only its process group.
func TestAToolStartsWithNoConsoleWindowAndAGameWithItsWindows(t *testing.T) {
	tool := exec.Command("x")
	configureLaunch(tool, profile.Action{})
	if tool.SysProcAttr.CreationFlags != createNewProcessGroup|noconsole.CreateNoWindow {
		t.Fatalf("tool flags %#x", tool.SysProcAttr.CreationFlags)
	}
	game := exec.Command("x")
	configureLaunch(game, profile.Action{SessionRole: profile.SessionClient})
	if game.SysProcAttr.CreationFlags != createNewProcessGroup {
		t.Fatalf("game flags %#x", game.SysProcAttr.CreationFlags)
	}
}
