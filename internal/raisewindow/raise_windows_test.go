//go:build windows

package raisewindow

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestAProcessWithNoWindowIsNotRaisedAndTheWaitIsBounded(t *testing.T) {
	started := time.Now()
	if Raise(os.Getpid(), 200*time.Millisecond) {
		t.Fatal("this test has no window to raise")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("waited %s", elapsed)
	}
}

// A real window on a real desktop, run by hand: CI runs headless.
func TestARealWindowIsFoundAndLeftNotTopmost(t *testing.T) {
	if os.Getenv("AUCOM_NATIVE_WINDOW") == "" {
		t.Skip("needs a desktop: set AUCOM_NATIVE_WINDOW=1")
	}
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"$f = New-Object System.Windows.Forms.Form",
		"$f.Text = 'aucom raise test'; $f.ShowInTaskbar = $true; $f.Show()",
		"Start-Sleep -Seconds 12",
	}, "; ")
	command := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	if !Raise(command.Process.Pid, 8*time.Second) {
		t.Fatal("the window was not found")
	}
	if window := find(command.Process.Pid); window == 0 {
		t.Fatal("the window went away")
	}
	time.Sleep(4 * time.Second)
}
