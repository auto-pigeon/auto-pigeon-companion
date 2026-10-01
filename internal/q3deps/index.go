package q3deps

import (
	"archive/zip"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Limits on what a scan will read.
//
// A scan walks directories somebody else laid out and opens archives somebody
// else made. None of the numbers here is a guess about the biggest real game:
// they are the point past which this stops being a scan and starts being a way
// to make the Companion sit in a loop.
const (
	maxFilesPerRoot   = 200000
	maxArchivesPerDir = 64
	maxArchiveEntries = 200000
	maxShaderBytes    = 8 << 20
	maxShaderScripts  = 512
)

// Where says which of the three places a file was found in. It is part of the
// answer and not a detail: "in the base game" and "in your own content" are
// different facts about the same missing PK3 member.
type Where string

const (
	// InArchive: the package about to be written carries it.
	InArchive Where = "archive"
	// InContent: it is on this machine, in content the user declared as their
	// own, and the package does not carry it.
	InContent Where = "content"
	// InBaseGame: it belongs to the installed game. Not to be packaged.
	InBaseGame Where = "base game"
)

// root is one game directory a scan looks in — a directory whose contents are
// what the engine sees, so `<something>/baseq3` or `<something>/mymod` rather
// than the directory those sit in.
type root struct {
	path string
	base bool
	// files maps a slash-separated VFS path to where it actually is: a file on
	// disk, or the archive that holds it.
	files map[string]string
	// truncated records that the walk hit a limit, so a `missing` verdict from
	// this root can say it might be wrong.
	truncated bool
}

// index is every root, in lookup order.
type index struct {
	archive map[string]string // member path -> source file on this machine
	roots   []*root
}

func newIndex(members map[string]string, contentRoots, gameRoots []string) (*index, error) {
	idx := &index{archive: map[string]string{}}
	for member, source := range members {
		idx.archive[normalizeFile(member)] = source
	}
	for _, dir := range contentRoots {
		r, err := indexRoot(dir, false)
		if err != nil {
			return nil, err
		}
		idx.roots = append(idx.roots, r)
	}
	for _, dir := range gameRoots {
		r, err := indexRoot(dir, true)
		if err != nil {
			return nil, err
		}
		idx.roots = append(idx.roots, r)
	}
	return idx, nil
}

func indexRoot(dir string, base bool) (*root, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("q3deps: %s: %w", dir, err)
	}
	r := &root{path: absolute, base: base, files: map[string]string{}}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		// A root that is not there is not an error: the user may have named a
		// directory for a game they have not installed, and the report says
		// what it could not find rather than refusing to run.
		return r, nil
	}
	archives := 0
	err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subdirectory is not a reason to stop
		}
		if entry.IsDir() {
			return nil
		}
		if len(r.files) >= maxFilesPerRoot {
			r.truncated = true
			return filepath.SkipAll
		}
		relative, relErr := filepath.Rel(absolute, path)
		if relErr != nil {
			return nil
		}
		vfs := normalizeFile(filepath.ToSlash(relative))
		if _, taken := r.files[vfs]; !taken {
			r.files[vfs] = path
		}
		if strings.HasSuffix(strings.ToLower(path), ".pk3") && archives < maxArchivesPerDir {
			archives++
			indexArchive(r, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("q3deps: reading %s: %w", dir, err)
	}
	return r, nil
}

// indexArchive records the names inside a PK3 without reading its content.
//
// Names only, and only for a base game root in practice: what a base-game
// shader script says does not matter, because the answer for anything defined
// there is already "the base game's, and not yours to ship". Opening the zip
// reads its central directory, not its files.
func indexArchive(r *root, path string) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return
	}
	defer reader.Close()
	for i, entry := range reader.File {
		if i >= maxArchiveEntries || len(r.files) >= maxFilesPerRoot {
			r.truncated = true
			return
		}
		if strings.HasSuffix(entry.Name, "/") {
			continue
		}
		vfs := normalizeFile(entry.Name)
		if _, taken := r.files[vfs]; !taken {
			r.files[vfs] = path + "!" + entry.Name
		}
	}
}

// located is one file a reference needs and where it turned out to be.
type located struct {
	// Path is the VFS path that was looked for.
	Path string `json:"path"`
	// Where is which of the three places held it. Empty when nowhere did.
	Where Where `json:"where,omitempty"`
	// Source is the file on this machine, or `archive!member` for one inside a
	// PK3 the scan looked in.
	Source string `json:"source,omitempty"`
	// Role says what this file is to the reference: `image`, `shader script`,
	// `model`, `sound`.
	Role string `json:"role"`
	// CompileOnly and RuntimeOnly say which program opens an image a shader
	// script names, and they are measured rather than assumed (Q3Map2 2.5.17n,
	// `Q3_011`): the COMPILER reads a shader's `qer_editorimage` or
	// `q3map_lightimage` and warns when it is absent even though every stage
	// image is there; an ENGINE reads the stage images and never opens the
	// editor one, and the compiler says nothing when a stage image is absent.
	// Neither set means both programs read it. A package review still counts
	// either kind as missing — these say WHICH half of the work would break.
	CompileOnly bool `json:"compile_only,omitempty"`
	RuntimeOnly bool `json:"runtime_only,omitempty"`
}

// File is one file a reference needs and where it turned out to be. It is the
// element type of [Resolution.Files], named so that a caller outside this
// package — the one that packages what a review resolved — can hold one.
type File = located

// Found reports whether anything on this machine has the file.
func (l located) Found() bool { return l.found() }

func (l located) found() bool { return l.Where != "" }

// find looks a VFS path up in the archive, then the content roots, then the
// base game — the order that decides which of the three answers a dependency
// gets, and the reason it is this order: a file the package carries is
// packaged whatever else also has a copy.
func (i *index) find(vfs, role string) located {
	vfs = normalizeFile(vfs)
	if source, packaged := i.archive[vfs]; packaged {
		return located{Path: vfs, Where: InArchive, Source: source, Role: role}
	}
	for _, r := range i.roots {
		if source, present := r.files[vfs]; present {
			where := InContent
			if r.base {
				where = InBaseGame
			}
			return located{Path: vfs, Where: where, Source: source, Role: role}
		}
	}
	return located{Path: vfs, Role: role}
}

// imageExtensions are what this scan will accept for a texture named without
// one.
//
// `.png` is here because Q3Map2 was measured finding one; whether the engine
// somebody plays your map in also reads it is a question about that engine, and
// this scan does not pretend to answer it.
var imageExtensions = []string{".tga", ".jpg", ".jpeg", ".png"}

// findImage resolves a texture name, which is usually written without an
// extension.
func (i *index) findImage(name string) located {
	name = normalizeFile(name)
	if extension := strings.ToLower(filepath.Ext(name)); extension != "" {
		for _, known := range imageExtensions {
			if extension == known {
				return i.find(name, "image")
			}
		}
	}
	for _, extension := range imageExtensions {
		if found := i.find(name+extension, "image"); found.found() {
			return found
		}
	}
	// Nothing found: report the name as written, with the extensions that were
	// tried named in the review rather than guessed at here.
	return located{Path: name, Role: "image"}
}

// truncatedRoots names the roots whose walk hit a limit.
func (i *index) truncatedRoots() []string {
	var out []string
	for _, r := range i.roots {
		if r.truncated {
			out = append(out, r.path)
		}
	}
	return out
}

// scriptRef is one shader script: where the engine sees it, and where it
// actually is on this machine.
//
// Both, because a report that named a shader script by its path on the author's
// disk would be a report carrying a home directory around — and the VFS path is
// what tells a reader whether the archive carries it.
type scriptRef struct {
	VFS    string
	Source string
}

// shaderScripts lists every `scripts/*.shader` this scan will parse, split into
// the ones whose contents are the user's — the archive's own and the content
// roots' — and the base game's.
//
// The split is the whole point. A shader the user defines has to be followed to
// the images it names, because those are files the package must carry. A shader
// the base game defines has to be RECOGNISED and then left alone: what it
// references is id's, and the answer a person needs is "this one is not yours
// to ship", not a list of files inside pak0.pk3.
func (i *index) shaderScripts() (content, base []scriptRef) {
	seen := map[string]bool{}
	add := func(into *[]scriptRef, ref scriptRef) {
		if len(*into) >= maxShaderScripts || seen[ref.Source] {
			return
		}
		seen[ref.Source] = true
		*into = append(*into, ref)
	}
	for _, member := range sortedKeys(i.archive) {
		if isShaderScript(member) {
			add(&content, scriptRef{VFS: member, Source: i.archive[member]})
		}
	}
	for _, r := range i.roots {
		for _, vfs := range sortedKeys(r.files) {
			if !isShaderScript(vfs) {
				continue
			}
			ref := scriptRef{VFS: vfs, Source: r.files[vfs]}
			if r.base {
				add(&base, ref)
				continue
			}
			add(&content, ref)
		}
	}
	return content, base
}

func sortedKeys(table map[string]string) []string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func isShaderScript(vfs string) bool {
	return strings.HasPrefix(vfs, "scripts/") && strings.HasSuffix(vfs, ".shader")
}
