package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Choosing what goes in, before anything is written.
//
// A plan is the whole of the decision: every file that would be packaged, where
// it came from, what the policy concluded and what a person still has to
// resolve. `companion package preview` prints one and stops. `companion package
// create` builds the same one, refuses if anything in it is unresolved, and
// only then opens a file.
//
// Nothing is written during planning, and every source is read exactly once —
// to hash it. That is what makes a preview safe to run against a directory
// somebody does not trust yet, and it is what makes the digest in the listing
// the digest of the bytes that will actually be packaged.

// DirSource is a directory to sweep.
type DirSource struct {
	// Dir is the directory on this machine.
	Dir string
	// Prefix is where its contents land inside the archive. Empty means the
	// archive root.
	Prefix string
}

// FileSource is one file placed at one member path.
type FileSource struct {
	// Path is the member path.
	Path string
	// File is where to read it from.
	File string
	// FromBuild names the build output this is, when it is one.
	FromBuild string
}

// Plan is what would be packaged.
type Plan struct {
	Target Target
	// Decisions is every candidate, in reading order: the ones needing
	// attention first.
	Decisions []Decision
	// Collisions are member paths that would confuse a reader. Always fatal
	// for a package this program writes — unlike inspection, where they are
	// reported about somebody else's archive.
	Collisions []Collision

	// sources maps a member path to where its bytes come from.
	sources map[string]FileSource
}

// Collect walks the selected sources and hashes everything they offer.
//
// Symbolic links are not followed and not packaged, in either role. A link that
// points inside the tree duplicates a file that is already being packaged under
// a second name; one that points outside it packages something the user did not
// select, and "the archive contained /etc/shadow because a link in the map
// directory pointed at it" is a sentence that must not be possible. Devices,
// sockets and pipes are refused for the same reason and a simpler one: there is
// nothing in them to package.
func Collect(dirs []DirSource, files []FileSource, target Target) ([]Candidate, error) {
	var candidates []Candidate
	claimed := map[string]string{}

	claim := func(memberPath, source string) error {
		if previous, taken := claimed[memberPath]; taken {
			return fmt.Errorf("pack: %q would be packaged twice, from %s and from %s", memberPath, previous, source)
		}
		claimed[memberPath] = source
		return nil
	}

	for _, dir := range dirs {
		root, err := filepath.Abs(dir.Dir)
		if err != nil {
			return nil, fmt.Errorf("pack: resolving %s: %w", dir.Dir, err)
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("pack: reading %s: %w", dir.Dir, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("pack: %s is not a directory", dir.Dir)
		}
		prefix := strings.Trim(strings.ReplaceAll(dir.Prefix, "\\", "/"), "/")

		walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("pack: reading %s: %w", path, err)
			}
			if path == root {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return fmt.Errorf("pack: %s is not inside %s", path, root)
			}
			name := filepath.ToSlash(relative)
			if prefix != "" {
				name = prefix + "/" + name
			}
			switch {
			case entry.IsDir():
				// Directories are not members: PAK has no record for one, and
				// a PK3's are created as the files inside them need them. The
				// name is still checked, so a directory that could not be
				// extracted is refused where it is understandable rather than
				// at the first file inside it.
				if err := CheckEntryPath(name, target.MaxNameLength); err != nil {
					return fmt.Errorf("%w: the directory %q %w", ErrUnsafePath, name, err)
				}
				return nil
			case entry.Type()&fs.ModeSymlink != 0:
				linkTarget, readErr := os.Readlink(path)
				if readErr != nil {
					linkTarget = "somewhere this program could not read"
				}
				return fmt.Errorf("%w: %s is a symbolic link to %q; a link in an archive is either a duplicate "+
					"under a second name or a way to package something that was not selected",
					ErrUnsafePath, relative, linkTarget)
			case !entry.Type().IsRegular():
				return fmt.Errorf("%w: %s is not a regular file (%s), and there is nothing in it to package",
					ErrUnsafePath, relative, entry.Type())
			}
			member, err := NormalizeEntryPath(name, target.MaxNameLength)
			if err != nil {
				return err
			}
			candidate, err := hashCandidate(member, path)
			if err != nil {
				return err
			}
			if err := claim(member, path); err != nil {
				return err
			}
			candidates = append(candidates, candidate)
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	for _, file := range files {
		member, err := NormalizeEntryPath(file.Path, target.MaxNameLength)
		if err != nil {
			return nil, err
		}
		source, err := filepath.Abs(file.File)
		if err != nil {
			return nil, fmt.Errorf("pack: resolving %s: %w", file.File, err)
		}
		info, err := os.Lstat(source)
		if err != nil {
			return nil, fmt.Errorf("pack: reading %s: %w", file.File, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: %s is a symbolic link", ErrUnsafePath, file.File)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%w: %s is not a regular file", ErrUnsafePath, file.File)
		}
		candidate, err := hashCandidate(member, source)
		if err != nil {
			return nil, err
		}
		candidate.FromBuild = file.FromBuild
		if err := claim(member, source); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
	return candidates, nil
}

// hashCandidate reads one file once, for its size and its digest.
func hashCandidate(member, source string) (Candidate, error) {
	file, err := os.Open(source)
	if err != nil {
		return Candidate{}, fmt.Errorf("pack: reading %s: %w", source, err)
	}
	defer file.Close()

	hash := sha256.New()
	// The +1 makes an oversized file an error rather than a silent truncation:
	// io.Copy with a LimitReader stops at the limit and reports success.
	size, err := io.Copy(hash, io.LimitReader(file, MaxEntrySize+1))
	if err != nil {
		return Candidate{}, fmt.Errorf("pack: reading %s: %w", source, err)
	}
	if size > MaxEntrySize {
		return Candidate{}, fmt.Errorf("pack: %s is over the %d bytes one member may be", source, int64(MaxEntrySize))
	}
	return Candidate{
		Path:   member,
		Source: source,
		Size:   size,
		SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

// NewPlan applies the policy to a set of candidates.
func NewPlan(candidates []Candidate, policy Policy, target Target) (*Plan, error) {
	plan := &Plan{Target: target, sources: map[string]FileSource{}}
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if err := CheckEntryPath(candidate.Path, target.MaxNameLength); err != nil {
			return nil, fmt.Errorf("%w: %q %w", ErrUnsafePath, candidate.Path, err)
		}
		decision := policy.Decide(candidate)
		if policy.Acknowledgements[candidate.Path] && decision.Verdict == Review {
			decision.Acknowledged = true
		}
		plan.Decisions = append(plan.Decisions, decision)
		plan.sources[candidate.Path] = FileSource{
			Path: candidate.Path, File: candidate.Source, FromBuild: candidate.FromBuild,
		}
		paths = append(paths, candidate.Path)
	}
	plan.Collisions = FindCollisions(paths)
	sortDecisions(plan.Decisions)
	return plan, nil
}

// Included is every decision that ends with the file in the archive.
func (p *Plan) Included() []Decision {
	var out []Decision
	for _, decision := range p.Decisions {
		if decision.Packaged() {
			out = append(out, decision)
		}
	}
	return out
}

// Excluded is every candidate that was offered and will not be packaged.
func (p *Plan) Excluded() []Decision {
	var out []Decision
	for _, decision := range p.Decisions {
		if !decision.Packaged() {
			out = append(out, decision)
		}
	}
	return out
}

// NeedsReview is every candidate a person still has to look at.
func (p *Plan) NeedsReview() []Decision {
	var out []Decision
	for _, decision := range p.Decisions {
		if decision.Verdict == Review && !decision.Acknowledged {
			out = append(out, decision)
		}
	}
	return out
}

// Refused is every candidate that needs an explicit authorization.
func (p *Plan) Refused() []Decision {
	var out []Decision
	for _, decision := range p.Decisions {
		if decision.Verdict == Refuse && !decision.Authorized {
			out = append(out, decision)
		}
	}
	return out
}

// TotalSize is what the packaged members add up to, uncompressed.
func (p *Plan) TotalSize() int64 {
	var total int64
	for _, decision := range p.Included() {
		total += decision.Size
	}
	return total
}

// Blocked reports why this plan may not be written, or nil.
//
// Three separate refusals with three separate messages, because they are three
// different situations for the person reading them: a collision is a mistake in
// the selection, an unreviewed file is a question waiting for an answer, and a
// refused one is a decision only the user can make.
func (p *Plan) Blocked() error {
	if len(p.Collisions) > 0 {
		return fmt.Errorf("pack: %s", p.Collisions[0].Error())
	}
	if refused := p.Refused(); len(refused) > 0 {
		return fmt.Errorf("pack: %d file(s) match a known released asset and were refused, starting with %q — %s. "+
			"Authorize each one explicitly if you hold the right to distribute it",
			len(refused), refused[0].Path, refused[0].Reason)
	}
	if review := p.NeedsReview(); len(review) > 0 {
		return fmt.Errorf("pack: %d file(s) need review before they can be packaged, starting with %q — %s. "+
			"Run `companion package preview` to see all of them, then acknowledge them",
			len(review), review[0].Path, review[0].Reason)
	}
	if len(p.Included()) == 0 {
		return fmt.Errorf("pack: nothing would be packaged")
	}
	return nil
}

// Members turns the plan into what a writer consumes, in the plan's own order.
func (p *Plan) Members() []Member {
	included := p.Included()
	members := make([]Member, 0, len(included))
	for _, decision := range included {
		source := p.sources[decision.Path]
		members = append(members, FileMember(decision.Path, source.File, decision.Size))
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Path < members[j].Path })
	return members
}

// SourcePaths is every file on this machine the plan would read, for the check
// that a package never overwrites its own input.
func (p *Plan) SourcePaths() []string {
	out := make([]string, 0, len(p.sources))
	for _, source := range p.sources {
		out = append(out, source.File)
	}
	sort.Strings(out)
	return out
}

// Preview renders the listing a person reviews: every path, where it came from,
// how big it is, and what was decided about it.
func (p *Plan) Preview() string {
	var b strings.Builder
	fmt.Fprintf(&b, "target %s — %s, %s, reproducibility %s\n",
		p.Target.ID, p.Target.Format, p.Target.Compression, p.Target.Reproducibility())
	fmt.Fprintf(&b, "%d file(s) selected, %s to package\n\n", len(p.Decisions), humanSize(p.TotalSize()))

	width := 0
	for _, decision := range p.Decisions {
		if len(decision.Path) > width {
			width = len(decision.Path)
		}
	}
	if width > 60 {
		width = 60
	}
	for _, decision := range p.Decisions {
		mark := " "
		switch {
		case decision.Verdict == Refuse && !decision.Authorized:
			mark = "!"
		case decision.Verdict == Review && !decision.Acknowledged:
			mark = "?"
		case decision.Acknowledged || decision.Authorized:
			mark = "+"
		}
		fmt.Fprintf(&b, "%s %-*s %10s  %-13s %s\n",
			mark, width, decision.Path, humanSize(decision.Size), decision.Provenance, decision.Rule)
		fmt.Fprintf(&b, "    from %s\n", decision.Source)
		fmt.Fprintf(&b, "    %s\n", decision.Reason)
		for _, hint := range decision.Hints {
			fmt.Fprintf(&b, "    hint: %s\n", hint)
		}
	}
	if len(p.Collisions) > 0 {
		b.WriteString("\ncollisions:\n")
		for _, collision := range p.Collisions {
			fmt.Fprintf(&b, "  %s\n", collision.Error())
		}
	}
	fmt.Fprintf(&b, "\n%d to package, %d awaiting review, %d refused\n",
		len(p.Included()), len(p.NeedsReview()), len(p.Refused()))
	return b.String()
}
