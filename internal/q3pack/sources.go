package q3pack

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3deps"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3vfs"
)

// LooseSourceID is the id of the one source that is not an archive: the loose
// files of the content folder.
const LooseSourceID = "loose"

// sources is every place the build's content came from, and the grants given
// for them.
type sources struct {
	byID map[string]*Source
	// archives maps a staged archive's path on this machine to its source id.
	archives map[string]string
	roots    stagedDirs
	grants   []Grant
	used     map[int]bool
}

// newSources lists the content the build staged: each archive, by the digest
// the build verified it at, and the loose files.
//
// The digests are the manifest's own. The build hashed each archive when it
// staged it and recorded which bytes the compiler was shown, so a grant given
// for a digest is a grant for those bytes and a file swapped since is refused
// when it is read — see [materialize].
func newSources(stage *q3vfs.Stage, roots stagedDirs, grants []Grant) *sources {
	out := &sources{
		byID:     map[string]*Source{},
		archives: map[string]string{},
		roots:    roots,
		grants:   grants,
		used:     map[int]bool{},
	}
	bound := map[string]q3vfs.Package{}
	for _, pkg := range stage.Packages {
		bound[hexDigest(pkg.SHA256)] = pkg
	}
	for _, root := range stage.Roots {
		if root.Role != q3vfs.PackagesRole {
			continue
		}
		for _, game := range root.Games {
			for _, archive := range game.Archives {
				digest := hexDigest(archive.SHA256)
				id := "archive:" + digest
				staged := filepath.Join(root.Path, game.Name, filepath.FromSlash(archive.Name))
				out.archives[filepath.Clean(staged)] = id
				if _, listed := out.byID[id]; listed {
					continue
				}
				source := &Source{
					ID: id, Kind: "archive", Name: archive.Name, Game: game.Name,
					SHA256: digest, Size: archive.Size,
				}
				if pkg, isBound := bound[digest]; isBound {
					source.Origin = pkg.Origin
				}
				if hint := archiveHint(archive.Name); hint != "" {
					source.Hints = append(source.Hints, hint)
				}
				out.byID[id] = source
			}
		}
	}
	out.byID[LooseSourceID] = &Source{ID: LooseSourceID, Kind: "loose", Name: "loose files in your content folder"}
	for _, source := range out.byID {
		source.Grant = out.sourceGrant(source.ID)
	}
	return out
}

// hexDigest is a digest in the one spelling grants and source ids use.
func hexDigest(digest string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(digest), "sha256:"))
}

// archiveHint is the observation about an archive's name, which decides nothing.
func archiveHint(name string) string {
	base := strings.ToLower(filepath.Base(name))
	stem, isPK3 := strings.CutSuffix(base, ".pk3")
	if digit, isPak := strings.CutPrefix(stem, "pak"); isPK3 && isPak && len(digit) == 1 && digit[0] >= '0' && digit[0] <= '9' {
		return fmt.Sprintf("%q is the naming id Software used for the archives it shipped; that is a name, "+
			"and it decides nothing — the grant is what decides", base)
	}
	return ""
}

// sourceGrant is the grant given for a whole source, or nil.
func (s *sources) sourceGrant(id string) *Grant {
	for i := range s.grants {
		grant := &s.grants[i]
		switch {
		case grant.Loose && id == LooseSourceID, grant.Archive != "" && id == "archive:"+grant.Archive:
			s.used[i] = true
			return grant
		}
	}
	return nil
}

// pathGrant is the grant given for one file, or nil.
func (s *sources) pathGrant(vfs string) *Grant {
	for i := range s.grants {
		if s.grants[i].Path == vfs {
			s.used[i] = true
			return &s.grants[i]
		}
	}
	return nil
}

// unusedGrants refuses a grant that names nothing this build read.
//
// A grant that silently applied to nothing would be an answer to a question
// nobody asked — and, typed with a digit wrong, an answer the person believes
// they gave for the archive beside it.
func (s *sources) unusedGrants() error {
	for i, grant := range s.grants {
		if s.used[i] {
			continue
		}
		if grant.Path != "" {
			return fmt.Errorf("q3pack: there is a grant for %s, and nothing an engine needs from this build's "+
				"content has that path. Check it against the dependency list", grant.Path)
		}
		return fmt.Errorf("q3pack: there is a grant for %s, and this build read no such archive. "+
			"The archives it read are listed under `sources`, each with its digest", grantTarget(grant))
	}
	return nil
}

// describe names a source in words, for a listing.
func (s *sources) describe(id string) string {
	source := s.byID[id]
	switch {
	case source == nil:
		return id
	case source.Kind == "loose":
		return "your content folder (a loose file)"
	case source.Origin != "":
		return fmt.Sprintf("%s/%s, a package the saved map is bound to", source.Game, source.Name)
	}
	return fmt.Sprintf("%s/%s in your content folder", source.Game, source.Name)
}

// list is every source, archives by name and the loose files last, leaving out
// a loose-files row nothing came from.
func (s *sources) list() []Source {
	out := make([]Source, 0, len(s.byID))
	for _, source := range s.byID {
		if source.Kind == "loose" && source.RuntimeFiles == 0 && source.Grant == nil {
			continue
		}
		out = append(out, *source)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// candidate decides what becomes of one content file an engine needs.
func (s *sources) candidate(file q3deps.File, target pack.Target) (*candidate, error) {
	entry := &candidate{vfs: file.Path, role: file.Role}
	archive, member, inside := q3deps.SplitSource(file.Source)
	if inside {
		id, known := s.archives[filepath.Clean(archive)]
		if !known {
			return nil, fmt.Errorf("q3pack: %s was found in %s, which is not an archive this build recorded "+
				"staging. Build the map again", file.Path, filepath.Base(archive))
		}
		entry.source, entry.archive, entry.entry, entry.member = id, archive, member, member
	} else {
		entry.source, entry.loose = LooseSourceID, file.Source
		relative, err := s.relative(file.Source)
		if err != nil {
			return nil, err
		}
		entry.member = relative
	}
	s.byID[entry.source].RuntimeFiles++

	entry.grant = s.pathGrant(file.Path)
	entry.pathGrant = entry.grant != nil
	if entry.grant == nil {
		entry.grant = s.byID[entry.source].Grant
	}
	switch {
	case entry.grant == nil:
		entry.disposition = ThirdPartyUnresolved
		entry.reason = "no grant covers " + s.describe(entry.source)
	case entry.grant.Basis == BasisNotRedistributable:
		entry.disposition = Blocked
		entry.reason = fmt.Sprintf("you said %s is not yours to redistribute", s.describe(entry.source))
		if entry.pathGrant {
			entry.reason = "you said this file is not yours to redistribute"
		}
	case entry.grant.Basis == BasisLicensed:
		entry.disposition = Licensed
	default:
		entry.disposition = UserAuthored
	}
	if entry.disposition.carried() {
		if normalized, err := pack.NormalizeEntryPath(entry.member, target.MaxNameLength); err != nil {
			entry.disposition = Blocked
			entry.reason = fmt.Sprintf("its path cannot be written into an archive safely: %v", err)
		} else {
			entry.member = normalized
		}
	}
	return entry, nil
}

// relative is a loose file's path inside its game directory, in the
// capitalisation the file has on disk.
func (s *sources) relative(source string) (string, error) {
	cleaned := filepath.Clean(source)
	for _, root := range s.roots.content {
		relative, err := filepath.Rel(root, cleaned)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		return filepath.ToSlash(relative), nil
	}
	return "", fmt.Errorf("q3pack: %s is not inside a content folder this build staged", source)
}

// materialize puts the bytes of every candidate a grant lets in somewhere
// `internal/pack` can read them as a regular file.
//
// A loose file was staged as a link to the user's own, and pack refuses a link
// — rightly, for a directory sweep — so it is resolved to the file it stands
// for. A member of an archive is read out with `pack.Extract`, which is the
// bounded, path-checked extractor: the archive is somebody else's, and its
// directory is a claim until it has been checked.
func materialize(candidates map[string]*candidate, order []string, work string) error {
	byArchive := map[string][]*candidate{}
	var archives []string
	for _, vfs := range order {
		entry := candidates[vfs]
		if !entry.disposition.carried() {
			continue
		}
		if entry.archive == "" {
			resolved, err := filepath.EvalSymlinks(entry.loose)
			if err != nil {
				entry.disposition, entry.reason = Blocked, fmt.Sprintf("it could not be read where the build staged it: %v", err)
				continue
			}
			info, err := os.Lstat(resolved)
			if err != nil || !info.Mode().IsRegular() {
				entry.disposition, entry.reason = Blocked, "it is not a regular file"
				continue
			}
			entry.file = resolved
			continue
		}
		if _, seen := byArchive[entry.archive]; !seen {
			archives = append(archives, entry.archive)
		}
		byArchive[entry.archive] = append(byArchive[entry.archive], entry)
	}
	sort.Strings(archives)
	for index, archive := range archives {
		entries := byArchive[archive]
		refuse := func(format string, args ...any) {
			for _, entry := range entries {
				entry.disposition, entry.reason = Blocked, fmt.Sprintf(format, args...)
			}
		}
		resolved, err := filepath.EvalSymlinks(archive)
		if err != nil {
			refuse("the archive it is in could not be read where the build staged it: %v", err)
			continue
		}
		// The digest the grant was given for, checked against the bytes that
		// are about to be read. A grant is for an archive's CONTENT.
		digest, _, err := hashFile(resolved)
		if err != nil {
			refuse("the archive it is in could not be read: %v", err)
			continue
		}
		if want := strings.TrimPrefix(entries[0].source, "archive:"); digest != want {
			refuse("the archive %s has changed since the build read it (it was %s and is now %s). Build the map again",
				filepath.Base(archive), want, digest)
			continue
		}
		// By the name the archive itself gives each member: what is read out is
		// selected by exactly what the scan found, not by a respelling of it.
		only := make([]string, 0, len(entries))
		for _, entry := range entries {
			only = append(only, entry.entry)
		}
		dest := filepath.Join(work, fmt.Sprintf("archive-%03d", index))
		if _, err := pack.Extract(resolved, pack.FormatPK3, pack.ExtractOptions{Dest: dest, Only: only}); err != nil {
			refuse("the archive %s could not be read safely: %v", filepath.Base(archive), err)
			continue
		}
		for _, entry := range entries {
			entry.file = filepath.Join(dest, filepath.FromSlash(entry.entry))
		}
	}
	return nil
}

// dependencies merges what the compile needed with what an engine needs into
// one list, worst first.
func dependencies(compile, runtime *q3deps.Report, candidates map[string]*candidate, optional map[string]bool) []Dependency {
	byKey := map[string]*Dependency{}
	var keys []string
	entry := func(reference q3deps.Reference) *Dependency {
		key := string(reference.Kind) + "\x00" + reference.Name
		if existing, seen := byKey[key]; seen {
			return existing
		}
		dependency := &Dependency{Name: reference.Name, Kind: string(reference.Kind), From: reference.From, Note: reference.Note}
		byKey[key] = dependency
		keys = append(keys, key)
		return dependency
	}
	addFile := func(dependency *Dependency, file File) {
		for i := range dependency.Files {
			if dependency.Files[i].Path == file.Path && dependency.Files[i].Role == file.Role {
				dependency.Files[i].Compile = dependency.Files[i].Compile || file.Compile
				dependency.Files[i].Runtime = dependency.Files[i].Runtime || file.Runtime
				if file.Runtime {
					dependency.Files[i].Disposition, dependency.Files[i].Source = file.Disposition, file.Source
					dependency.Files[i].Member, dependency.Files[i].Reason = file.Member, file.Reason
				}
				return
			}
		}
		dependency.Files = append(dependency.Files, file)
	}
	// located is a file as the scan found it, before any grant: where it is.
	located := func(found q3deps.File) File {
		file := File{Path: found.Path, Role: found.Role}
		switch {
		case !found.Found():
			file.Disposition = Missing
		case found.Where == q3deps.InBaseGame:
			file.Disposition = BaseGame
		default:
			file.Disposition = CompileOnly
			if decided := candidates[found.Path]; decided != nil {
				file.Source = decided.source
			}
		}
		return file
	}

	if compile != nil {
		for _, resolution := range compile.Resolutions {
			if resolution.RuntimeOnly {
				continue // the engine's half lists it, from the BSP
			}
			dependency := entry(resolution.Reference)
			dependency.RequiredAtCompile = true
			for _, found := range resolution.Files {
				if found.RuntimeOnly {
					continue
				}
				file := located(found)
				file.Compile = true
				addFile(dependency, file)
			}
		}
	}
	for _, resolution := range runtime.Resolutions {
		if optional[resolution.Name] && resolution.Status == q3deps.StatusMissing {
			continue // a level shot that is not there is not a missing file
		}
		dependency := entry(resolution.Reference)
		dependency.RequiredAtRuntime = true
		if dependency.From != resolution.From && !strings.Contains(dependency.From, resolution.From) {
			dependency.From += "; " + resolution.From
		}
		for _, found := range resolution.Files {
			if found.CompileOnly {
				continue
			}
			file := located(found)
			file.Runtime = true
			if decided := candidates[found.Path]; decided != nil && found.Where == q3deps.InContent {
				file.Disposition, file.Source, file.Reason = decided.disposition, decided.source, decided.reason
				if decided.disposition.carried() {
					file.Member = decided.member
				}
			}
			addFile(dependency, file)
		}
	}

	out := make([]Dependency, 0, len(keys))
	for _, key := range keys {
		dependency := byKey[key]
		dependency.Disposition = CompileOnly
		worst := -1
		for _, file := range dependency.Files {
			if !file.Runtime {
				continue
			}
			if worst < 0 || rank[file.Disposition] < worst {
				worst = rank[file.Disposition]
				dependency.Disposition = file.Disposition
			}
		}
		sort.SliceStable(dependency.Files, func(i, j int) bool { return dependency.Files[i].Path < dependency.Files[j].Path })
		out = append(out, *dependency)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Disposition] != rank[out[j].Disposition] {
			return rank[out[i].Disposition] < rank[out[j].Disposition]
		}
		return out[i].Name < out[j].Name
	})
	return out
}
