// Package builtin holds the profile documents that ship inside the Companion.
//
// # Built in means "arrived with the program", and nothing else
//
// These documents are compiled in with //go:embed, so trusting them is the same
// act as trusting the binary: a user who installed this build has already made
// that decision, which is why [profile.Authorize] does not ask again for a
// built-in profile.
//
// What "built in" does *not* mean is a shortcut. Every document here is read by
// the same decoder, validated by the same rules, canonicalized by the same
// encoder and resolved by the same [profile.Resolve] as a file somebody
// downloads. `TestBuiltinAndUserAuthoredResolveIdentically` exists to keep that
// honest: it resolves a built-in sample and a user-authored equivalent and
// compares the resulting commands. If a privileged path ever appears, that test
// is what fails.
//
// # These three are samples
//
// They are named `sample.…` deliberately. The qualified EricW toolchain and the
// curated engine profiles are added by the tasks that qualify them against
// upstream releases; presenting an unqualified sample as though it were one of
// those would be exactly the kind of plausible-looking wrong answer the rest of
// this repository is careful about. They exist so the format has complete,
// valid, readable examples that the tests exercise.
package builtin

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

//go:embed *.json
var files embed.FS

// Entry is one built-in document: what it says, what file it came from, and
// the digest of its canonical form.
type Entry struct {
	// File is the embedded file name, for error messages.
	File string
	// Profile is the decoded, validated document.
	Profile profile.Profile
	// Digest is the canonical digest. Recorded here so a binding can be written
	// against it exactly as one is for an imported profile.
	Digest string
}

// Trust is the state every entry here has.
func (e Entry) Trust() profile.Trust { return profile.TrustBuiltin }

// Load decodes every built-in document.
//
// It returns an error rather than panicking at init, and callers report it,
// because a build whose own profiles do not parse should say so where a user
// can see it — not fail to start with a stack trace.
func Load() ([]Entry, error) {
	names, err := fs.Glob(files, "*.json")
	if err != nil {
		return nil, fmt.Errorf("builtin: listing the embedded profiles: %w", err)
	}
	sort.Strings(names)

	entries := make([]Entry, 0, len(names))
	seen := map[string]string{}
	for _, name := range names {
		data, err := files.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("builtin: reading %s: %w", name, err)
		}
		p, err := profile.Decode(data)
		if err != nil {
			return nil, fmt.Errorf("builtin: %s is not a valid profile: %w", name, err)
		}
		digest, err := profile.Digest(p)
		if err != nil {
			return nil, fmt.Errorf("builtin: %s cannot be digested: %w", name, err)
		}
		id := p.Metadata().ID
		if first, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("builtin: %s and %s both declare the id %q", first, name, id)
		}
		seen[id] = name
		entries = append(entries, Entry{File: name, Profile: p, Digest: digest})
	}
	return entries, nil
}

// Find returns the built-in profile with an id.
func Find(id string) (Entry, error) {
	entries, err := Load()
	if err != nil {
		return Entry{}, err
	}
	for _, e := range entries {
		if e.Profile.Metadata().ID == id {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("builtin: no built-in profile has the id %q", id)
}
