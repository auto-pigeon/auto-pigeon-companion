//go:build !linux && !windows

package job

import "errors"

// Outside Linux and Windows the Companion has no dependency-free way to read a
// process's CPU time and resident memory (macOS's proc_pidinfo needs cgo), so
// a reading says it is unavailable rather than inventing a zero.
func newTreeReader(int, uint64) (treeReader, string, error) {
	return nil, "", errors.New("CPU and memory are not measured on this platform")
}
