//go:build !windows

package cli

import (
	"os"
	"syscall"
)

// terminateSelf signals this process the way Ctrl-C does, which is how
// TestNoSubcommandStartsTheServerAndOpensTheBrowser proves the server shuts
// down gracefully and takes its token file with it.
//
// It lives behind a build tag because `syscall.Kill` does not exist on Windows.
// The test already skipped there at runtime, but a runtime skip cannot rescue a
// symbol that fails to COMPILE: the whole `internal/cli` test package stopped
// building on windows/amd64, so none of its tests ran there at all and
// `go vet ./...` — which AGENTS.md §2 requires to pass before a task is
// complete — could not pass on a Windows host. Found by AUCOM/AUT 246I while
// running the gate on Windows for the first time.
func terminateSelf() error { return syscall.Kill(os.Getpid(), syscall.SIGTERM) }
