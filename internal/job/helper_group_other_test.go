//go:build !linux

package job

// groupMembers reports -1: this platform is not asked to enumerate a process
// group, and inventing a count from something else would be the substitution
// `AUCOM/AUT 229` says not to make. The caller checks group liveness instead
// and records which of the two it managed.
func groupMembers(int) int { return -1 }
