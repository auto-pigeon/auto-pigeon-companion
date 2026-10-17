package q2deps

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/pack"
)

// Origin says how a file is held in a game folder.
type Origin string

const (
	// Loose is a file on disk under the folder.
	Loose Origin = "loose"
	// Archived is a member of a PAK directly in the folder.
	Archived Origin = "archive"
)

// Location is where one virtual path is in a game folder.
type Location struct {
	// Path is the virtual path as the folder spells it.
	Path   string `json:"path"`
	Origin Origin `json:"origin"`
	// File is the loose file on this machine, through any link, for Loose.
	File string `json:"-"`
	// Archive is the archive's name in the folder, for Archived.
	Archive string `json:"archive,omitempty"`
	Size    int64  `json:"size"`
}

// Index is what one game folder holds, the way Quake II's filesystem reads it.
type Index struct {
	Root string
	// Archives are the PAKs read, by name.
	Archives []string
	// Limits is what was not read.
	Limits []string

	loose    map[string]Location   // by exact virtual path
	folded   map[string][]Location // every spelling on disk, by lower-cased virtual path
	archived map[string]Location   // by lower-cased member path; the first archive in engine order wins
	files    int
	// stat is how this machine answers an open of a path: os.Stat. A test
	// replaces it to ask what a case-insensitive filesystem would answer.
	stat func(string) (os.FileInfo, error)
}

// maxIndexFiles bounds one folder's walk.
const maxIndexFiles = 250000

// IndexDir reads a game folder: its loose files, and the members of every PAK
// directly in it.
//
// Measured in Yamagi Quake II 8.70's filesystem: a loose file is found under
// the name asked for or, failing that, the name lower-cased — each an `fopen`,
// so whether another spelling of a name opens a file is the filesystem's
// answer and not the engine's (see [Index.FindLoose]); an archive member
// is found whatever its case; and within one folder the archives are searched
// before the loose files, numbered `pak<N>` from the highest down after any
// other `.pak`. Links are followed, as the engine follows them. An archive
// below the top of the folder is a file like any other, and is not opened.
func IndexDir(root string) (*Index, error) {
	index := &Index{Root: root, loose: map[string]Location{}, folded: map[string][]Location{},
		archived: map[string]Location{}, stat: os.Stat}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("q2deps: reading %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("q2deps: %s is not a folder", root)
	}
	var archives []string
	err = walk(root, "", func(rel, abs string, size int64) error {
		index.files++
		if index.files > maxIndexFiles {
			return fmt.Errorf("q2deps: %s holds more than %d files; this review reads no more", root, maxIndexFiles)
		}
		location := Location{Path: rel, Origin: Loose, File: abs, Size: size}
		index.loose[rel] = location
		index.folded[strings.ToLower(rel)] = append(index.folded[strings.ToLower(rel)], location)
		if !strings.Contains(rel, "/") {
			switch strings.ToLower(filepath.Ext(rel)) {
			case ".pak":
				archives = append(archives, rel)
			case ".pk3", ".pk2", ".pkz", ".zip":
				index.Limits = append(index.Limits, fmt.Sprintf("%s is a zip archive; this review does not open it", rel))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, name := range engineArchiveOrder(archives) {
		// Read for lookup: a member named `a/../b` is skipped, not a reason to
		// lose the archive (Quake II's own pak0.pak has two, Q2_020).
		inspection, err := pack.InspectForLookup(filepath.Join(root, name), pack.FormatPAK, pack.Budget{})
		if err != nil {
			index.Limits = append(index.Limits, fmt.Sprintf("%s could not be read as a PAK: %v", name, err))
			continue
		}
		index.Archives = append(index.Archives, name)
		if len(inspection.Skipped) > 0 {
			index.Limits = append(index.Limits, fmt.Sprintf(
				"%s: %d member(s) whose names are not plain paths were not indexed, so a reference spelled that way is reported missing rather than resolved: %s",
				name, len(inspection.Skipped), strings.Join(inspection.Skipped, "; ")))
		}
		for _, entry := range inspection.Entries {
			key := strings.ToLower(entry.Path)
			if _, taken := index.archived[key]; taken {
				continue
			}
			index.archived[key] = Location{Path: entry.Path, Origin: Archived, Archive: name, Size: entry.Size}
		}
	}
	return index, nil
}

// engineArchiveOrder is the order Yamagi searches one folder's archives in:
// the archives that are not `pak<N>` first, then `pak<N>` from the highest N.
// The first is the one whose copy of a path the engine uses.
func engineArchiveOrder(names []string) []string {
	var other, numbered []string
	number := map[string]int{}
	for _, name := range names {
		lower := strings.ToLower(name)
		var n int
		if _, err := fmt.Sscanf(lower, "pak%d.pak", &n); err == nil && fmt.Sprintf("pak%d.pak", n) == lower {
			numbered = append(numbered, name)
			number[name] = n
			continue
		}
		other = append(other, name)
	}
	sort.Strings(other)
	sort.Slice(numbered, func(i, j int) bool { return number[numbered[i]] > number[numbered[j]] })
	return append(other, numbered...)
}

// Find is where the folder holds a virtual path, the way the engine looks:
// archives first, then the loose file.
func (i *Index) Find(virtual string) (Location, bool) {
	if i == nil {
		return Location{}, false
	}
	if location, ok := i.archived[strings.ToLower(virtual)]; ok {
		return location, true
	}
	return i.FindLoose(virtual)
}

// FindLoose is the loose file only, under the two spellings Yamagi opens: the
// name as asked for, then lower-cased.
//
// Each is an `fopen`, so what a spelling opens is the FILESYSTEM's answer. On
// a case-sensitive one (Linux, as a rule) that is the file spelled exactly so,
// and nothing else. On a case-insensitive one (Windows, macOS by default) it is
// the file whatever the case of each folder and name — and two folders a
// sensitive filesystem keeps apart, `Mine/` and `mine/`, are one there, under
// whichever spelling was created first (Q2_020: CI at 7b7524d). So a spelling
// the index does not hold exactly is asked of this machine, and the answer is
// matched to an indexed file by identity rather than by name. Nothing is
// case-folded on a filesystem that does not fold.
func (i *Index) FindLoose(virtual string) (Location, bool) {
	if i == nil {
		return Location{}, false
	}
	if location, ok := i.opens(virtual); ok {
		return location, true
	}
	return i.opens(strings.ToLower(virtual))
}

// opens is the loose file one spelling opens on this machine.
func (i *Index) opens(spelling string) (Location, bool) {
	if location, ok := i.loose[spelling]; ok {
		return location, true
	}
	// Only the files the walk read can answer, so a spelling that is not some
	// case of an indexed path — or one that climbs out of the folder — never
	// reaches the filesystem at all.
	candidates := i.folded[strings.ToLower(spelling)]
	if len(candidates) == 0 || i.stat == nil {
		return Location{}, false
	}
	opened, err := i.stat(filepath.Join(i.Root, filepath.FromSlash(spelling)))
	if err != nil || !opened.Mode().IsRegular() {
		return Location{}, false
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate.File); err == nil && os.SameFile(opened, info) {
			return candidate, true
		}
	}
	return Location{}, false
}

// LooseFiles is the number of loose files read.
func (i *Index) LooseFiles() int { return len(i.loose) }

func walk(root, prefix string, visit func(rel, abs string, size int64) error) error {
	dir := filepath.Join(root, filepath.FromSlash(prefix))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("q2deps: reading %s: %w", dir, err)
	}
	for _, entry := range entries {
		rel := entry.Name()
		if prefix != "" {
			rel = prefix + "/" + entry.Name()
		}
		abs := filepath.Join(dir, entry.Name())
		info, err := os.Stat(abs) // through a link, as the engine reads it
		if err != nil {
			continue // a dangling link is no file
		}
		switch {
		case info.IsDir():
			if entry.Type()&fs.ModeSymlink != 0 && strings.Count(rel, "/") > 8 {
				continue
			}
			if err := walk(root, rel, visit); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			if err := visit(rel, abs, info.Size()); err != nil {
				return err
			}
		}
	}
	return nil
}
