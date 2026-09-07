package acquire

import (
	"fmt"
	"sort"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
)

// Garbage collection, and the direction of these imports.
//
// This file imports internal/binding and internal/job, and neither may ever
// import this package. That is not a style preference, it is what makes the
// collector correct: deleting a cache entry is safe only if *every* thing that
// could refer to one has been asked, and the only way to be sure of that is for
// the asking to happen in one function that names them all. A collector that
// took an opaque "keep set" from its caller would be a collector whose
// correctness depended on each caller remembering the whole list.
//
// The direction also encodes a real rule about the executor: a job never
// acquires anything. Executables reach a job through the request the caller
// built from a binding, resolved before the job was submitted, so internal/job
// has no reason to reach for a downloader — and cannot.

// Reference is one reason a cache entry must be kept.
type Reference struct {
	Digest string
	// Kind is `binding` or `job`.
	Kind string
	// ID is the profile id or the job id that holds the reference.
	ID string
}

// ReferencesFrom is every reason every cache entry has to stay.
//
// Both sources matter and for different reasons. A binding is a live
// dependency: a profile bound to a downloaded toolchain stops working the
// moment it is collected. A job record is evidence: it says what ran, and a
// record whose toolchain has been deleted can no longer answer the question it
// was kept to answer. Retention is the user's decision, expressed by not having
// deleted the job.
func ReferencesFrom(bindings *binding.Set, jobs []*job.Job) []Reference {
	var references []Reference
	if bindings != nil {
		for _, b := range bindings.Bindings {
			for _, install := range b.Installs {
				references = append(references, Reference{Digest: install.Digest, Kind: "binding", ID: b.ProfileID})
			}
		}
	}
	for _, j := range jobs {
		for _, digest := range j.Installs {
			references = append(references, Reference{Digest: digest, Kind: "job", ID: j.ID})
		}
	}
	sort.Slice(references, func(i, k int) bool {
		if references[i].Digest != references[k].Digest {
			return references[i].Digest < references[k].Digest
		}
		if references[i].Kind != references[k].Kind {
			return references[i].Kind < references[k].Kind
		}
		return references[i].ID < references[k].ID
	})
	return references
}

// Collected is what a garbage collection did, or would do.
type Collected struct {
	// Kept and Removed are cache entries, by digest.
	Kept    []KeptEntry
	Removed []*Install
	// Staging is the abandoned staging directories that were cleaned up.
	Staging []string
	// DryRun records that nothing was actually deleted.
	DryRun bool
}

// KeptEntry is one entry that survived, and why.
type KeptEntry struct {
	Install *Install
	By      []Reference
}

// stagingGrace is how old an abandoned staging directory has to be before it is
// removed. Long enough that a slow download in another process is not swept out
// from under itself.
const stagingGrace = 24 * time.Hour

// Collect removes every cache entry nothing refers to.
//
// Unreferenced and nothing else. Not "older than", not "over a size budget",
// not "not the newest version": every one of those would eventually delete
// something a user had deliberately pinned, and a build tool that silently
// removes the compiler a project is pinned to is a build tool that breaks a
// build for reasons nobody can reconstruct. If it is referenced it stays, for
// as long as the reference does.
func (a *Acquirer) Collect(references []Reference, dryRun bool) (*Collected, error) {
	installs, err := a.cache.List()
	if err != nil {
		return nil, err
	}
	by := map[string][]Reference{}
	for _, reference := range references {
		by[reference.Digest] = append(by[reference.Digest], reference)
	}
	result := &Collected{DryRun: dryRun}
	for _, install := range installs {
		if held := by[install.Digest]; len(held) > 0 {
			result.Kept = append(result.Kept, KeptEntry{Install: install, By: held})
			continue
		}
		if !dryRun {
			if err := a.cache.Remove(install.Digest); err != nil {
				return nil, err
			}
		}
		result.Removed = append(result.Removed, install)
	}
	if !dryRun {
		staging, err := a.cache.CleanStaging(stagingGrace, a.now())
		if err != nil {
			return result, err
		}
		result.Staging = staging
	}
	return result, nil
}

// Text renders a collection for a person.
func (c *Collected) Text() string {
	if len(c.Kept) == 0 && len(c.Removed) == 0 && len(c.Staging) == 0 {
		return "The cache is empty.\n"
	}
	var out string
	if c.DryRun {
		out += "Nothing was removed: this was a dry run.\n\n"
	}
	for _, kept := range c.Kept {
		out += fmt.Sprintf("keep    %s %s (%s)\n", kept.Install.PackageID, kept.Install.Version, kept.Install.Digest)
		for _, reference := range kept.By {
			out += fmt.Sprintf("          held by %s %s\n", reference.Kind, reference.ID)
		}
	}
	for _, removed := range c.Removed {
		verb := "remove "
		if c.DryRun {
			verb = "would remove "
		}
		out += fmt.Sprintf("%s %s %s (%s) — nothing refers to it\n", verb, removed.PackageID, removed.Version, removed.Digest)
	}
	for _, staged := range c.Staging {
		out += fmt.Sprintf("remove  abandoned download %s\n", staged)
	}
	return out
}
