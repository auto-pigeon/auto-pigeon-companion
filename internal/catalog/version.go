package catalog

import (
	"sort"
	"strings"
)

// Ordering upstream version strings.
//
// A package's version is upstream's spelling, not a semantic version: the tools
// this catalogue describes number themselves `2.0.0-alpha`, `1.5rc1`, `20240115`
// and `v2`, and forcing them into MAJOR.MINOR.PATCH would mean either refusing
// real releases or rewriting them into something that no longer matches what
// upstream published.
//
// So the ordering here is a natural-order comparison — digit runs compared as
// numbers, everything else as text — and it is used for exactly one thing:
// picking the newest version when the user did not name one. Every other
// decision in this package is by exact string match or by digest, because those
// are the ones where being wrong matters.
func compareVersions(a, b string) int {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		if isDigit(a[ai]) && isDigit(b[bi]) {
			aStart, bStart := ai, bi
			for ai < len(a) && isDigit(a[ai]) {
				ai++
			}
			for bi < len(b) && isDigit(b[bi]) {
				bi++
			}
			an := strings.TrimLeft(a[aStart:ai], "0")
			bn := strings.TrimLeft(b[bStart:bi], "0")
			if len(an) != len(bn) {
				if len(an) < len(bn) {
					return -1
				}
				return 1
			}
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
			continue
		}
		if a[ai] != b[bi] {
			if a[ai] < b[bi] {
				return -1
			}
			return 1
		}
		ai++
		bi++
	}
	// One ran out. A remainder that starts with a dash is a pre-release, and a
	// pre-release comes *before* the release it leads to: 1.0.0-rc1 is not
	// newer than 1.0.0, however the strings compare. That is the one place
	// this comparison departs from plain natural order, and it is the case
	// that would otherwise install a release candidate over a release.
	switch {
	case ai < len(a):
		if a[ai] == '-' {
			return -1
		}
		return 1
	case bi < len(b):
		if b[bi] == '-' {
			return 1
		}
		return -1
	}
	return 0
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func laterVersion(a, b string) bool { return compareVersions(a, b) > 0 }

// LaterVersion reports whether a is a later upstream version than b. Exported
// for the acquirer, which picks between two *installed* versions with no
// catalogue in hand — the offline case.
func LaterVersion(a, b string) bool { return laterVersion(a, b) }

func sortVersionsDescending(versions []string) {
	sort.Slice(versions, func(i, j int) bool { return compareVersions(versions[i], versions[j]) > 0 })
}
