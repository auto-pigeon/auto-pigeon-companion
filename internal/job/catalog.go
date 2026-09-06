package job

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile/builtin"
)

// Where a profile comes from, and what that says about trusting it.
//
// Two places, and the difference between them is the whole of the trust model
// this package inherits from internal/profile. A document compiled into the
// binary arrived with the program: trusting it is the same act as installing
// the build, which the user already did. A document in a directory arrived some
// other way, and nothing here knows how — so it is `local` until somebody says
// otherwise, and [profile.Authorize] refuses to run it without a recorded
// grant.
//
// There is no third case and no privileged path. Both go through the same
// decoder, the same validation, the same canonical digest and the same
// resolver.

// CatalogEntry is one profile the executor can be asked to run.
type CatalogEntry struct {
	Profile profile.Profile
	Trust   profile.Trust
	// Digest is the canonical digest, which is what a grant is recorded
	// against.
	Digest string
	// Source is where it came from, for a message a user can act on: the
	// embedded file name, or the path on disk.
	Source string
}

// Catalog is where the executor looks a profile up.
type Catalog interface {
	Lookup(id string) (CatalogEntry, error)
	List() ([]CatalogEntry, error)
}

// ErrNoProfile reports that no profile has an id.
var ErrNoProfile = errors.New("job: no such profile")

// DirCatalog is the built-in profiles plus any in a directory.
//
// Dir may be empty, which is the normal case today: nothing has been imported
// yet, and the built-in samples are what there is.
type DirCatalog struct {
	Dir string
}

// NewCatalog returns the catalog rooted at a directory of user profiles.
func NewCatalog(dir string) *DirCatalog { return &DirCatalog{Dir: dir} }

// List returns every profile, built-ins first, each sorted by id.
func (c *DirCatalog) List() ([]CatalogEntry, error) {
	entries, err := builtin.Load()
	if err != nil {
		return nil, err
	}
	out := make([]CatalogEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, CatalogEntry{
			Profile: entry.Profile,
			Trust:   profile.TrustBuiltin,
			Digest:  entry.Digest,
			Source:  "built in (" + entry.File + ")",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Profile.Metadata().ID < out[j].Profile.Metadata().ID })

	local, err := c.readDir()
	if err != nil {
		return nil, err
	}
	known := map[string]string{}
	for _, entry := range out {
		known[entry.Profile.Metadata().ID] = entry.Source
	}
	for _, entry := range local {
		id := entry.Profile.Metadata().ID
		if first, duplicate := known[id]; duplicate {
			// Refused rather than resolved by precedence. "Which of these two
			// documents ran" is not a question a user should have to work out
			// from a shadowing rule.
			return nil, fmt.Errorf("job: %s and %s both declare the profile id %q", first, entry.Source, id)
		}
		known[id] = entry.Source
		out = append(out, entry)
	}
	return out, nil
}

// Lookup finds one profile by id.
func (c *DirCatalog) Lookup(id string) (CatalogEntry, error) {
	entries, err := c.List()
	if err != nil {
		return CatalogEntry{}, err
	}
	for _, entry := range entries {
		if entry.Profile.Metadata().ID == id {
			return entry, nil
		}
	}
	available := make([]string, 0, len(entries))
	for _, entry := range entries {
		available = append(available, entry.Profile.Metadata().ID)
	}
	if len(available) == 0 {
		return CatalogEntry{}, fmt.Errorf("%w: %q", ErrNoProfile, id)
	}
	return CatalogEntry{}, fmt.Errorf("%w: %q (this build has: %s)", ErrNoProfile, id, strings.Join(available, ", "))
}

// readDir decodes every profile document in the catalog's directory.
func (c *DirCatalog) readDir() ([]CatalogEntry, error) {
	if strings.TrimSpace(c.Dir) == "" {
		return nil, nil
	}
	names, err := os.ReadDir(c.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("job: listing %s: %w", c.Dir, err)
	}
	var out []CatalogEntry
	for _, name := range names {
		if name.IsDir() || !strings.HasSuffix(name.Name(), ".json") {
			continue
		}
		path := filepath.Join(c.Dir, name.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("job: reading %s: %w", path, err)
		}
		p, err := profile.Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("job: %s is not a valid profile: %w", path, err)
		}
		digest, err := profile.Digest(p)
		if err != nil {
			return nil, fmt.Errorf("job: %s cannot be digested: %w", path, err)
		}
		out = append(out, CatalogEntry{
			Profile: p,
			// `local` until an import records something better. A file that
			// appeared in a directory is not a file anybody vouched for, and
			// treating it as one is the mistake the trust states exist to stop.
			Trust:  profile.TrustLocal,
			Digest: digest,
			Source: path,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Profile.Metadata().ID < out[j].Profile.Metadata().ID })
	return out, nil
}

// Chain is several catalogs read as one, first match winning by order.
//
// Order is the resolution rule and it is deliberately not "most specific" or
// "highest trust": the first catalog in the chain is the authority for any id
// it has. That makes "which document ran" answerable by reading the chain, and
// [DirCatalog] still refuses two documents with the same id inside itself, so
// the only shadowing that can happen is between chain members a caller chose.
type Chain []Catalog

// Lookup returns the first catalog's entry for an id.
func (c Chain) Lookup(id string) (CatalogEntry, error) {
	var first error
	for _, catalog := range c {
		entry, err := catalog.Lookup(id)
		if err == nil {
			return entry, nil
		}
		if first == nil || !errors.Is(err, ErrNoProfile) {
			// A catalog that failed for a reason other than "not here" is
			// reported rather than skipped: a profile directory that cannot be
			// read is something the user needs to know about, not a silent
			// fallthrough to the next source.
			if !errors.Is(err, ErrNoProfile) {
				return CatalogEntry{}, err
			}
			first = err
		}
	}
	if first == nil {
		return CatalogEntry{}, fmt.Errorf("%w: %q", ErrNoProfile, id)
	}
	return CatalogEntry{}, first
}

// List returns every entry, in chain order, skipping ids an earlier catalog
// already claimed.
func (c Chain) List() ([]CatalogEntry, error) {
	var out []CatalogEntry
	seen := map[string]bool{}
	for _, catalog := range c {
		entries, err := catalog.List()
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			id := entry.Profile.Metadata().ID
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, entry)
		}
	}
	return out, nil
}
