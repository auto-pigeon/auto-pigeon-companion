//go:build darwin

package pathpick

import (
	"context"
	"os/exec"
	"time"
)

// quarantined asks the system's own xattr tool, which every macOS has, whether
// the file carries com.apple.quarantine. No cgo: the Companion stays a plain
// cross-compiled binary.
func quarantined(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/usr/bin/xattr", "-p", "com.apple.quarantine", path).Run() == nil
}
