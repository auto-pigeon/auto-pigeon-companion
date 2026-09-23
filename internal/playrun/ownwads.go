package playrun

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Your own copy of a WAD Auto-Pigeon may not hand out.
//
// Most Quake maps name one of id Software's texture WADs — metal.wad, base.wad
// — and an Auto-Pigeon deployment holds its copy of those but may not
// redistribute it, so the bundle arrives without it and AUB says the map is
// not compiler-ready (`wad_bytes_not_carried`). Before this, such a map could
// not be built by Build & Run at all, however many copies of Quake the person
// owned.
//
// The rule that did not change: the Companion never QUIETLY uses a similarly
// named WAD from the person's game folder. Here the person names a folder, in
// the review, for this run; only the WADs AUB refused to carry are taken from
// it, each by its exact file name; and every one is recorded — name, SHA-256,
// size — in the run and in the build manifest, beside the bundle it completed.
// A refusal of any other kind (a private source, a texture nobody supplies)
// still stops the run: an own copy is an answer to "may not redistribute" and
// to nothing else.

// OwnWADsNeeded reports the WADs a bundle lacks only because AUB may not
// redistribute them, and whether that is the only thing wrong with it.
//
// `wad_inventory_incomplete` accompanies a WAD that was not carried — AUB
// cannot list the contents of bytes it does not ship — so it does not count as
// a separate problem when at least one WAD was refused for redistribution.
func OwnWADsNeeded(refusals []string) ([]string, bool) {
	var names []string
	for _, refusal := range refusals {
		code, subject, _ := strings.Cut(refusal, ":")
		switch strings.TrimSpace(code) {
		case "wad_bytes_not_carried":
			name := wadFileName(subject)
			if name == "" {
				return nil, false
			}
			names = append(names, name)
		case "wad_inventory_incomplete":
		default:
			return nil, false
		}
	}
	if len(names) == 0 {
		return nil, false
	}
	sort.Strings(names)

	return uniqueStrings(names), true
}

// wadFileName is the file name a declared WAD is looked up by: its last path
// element, whichever separator the map was written with.
func wadFileName(declared string) string {
	declared = strings.TrimSpace(declared)
	if i := strings.LastIndexAny(declared, `/\`); i >= 0 {
		declared = declared[i+1:]
	}
	if declared == "" || declared == "." || declared == ".." {
		return ""
	}

	return declared
}

// OwnWAD is one WAD found in the folder a person named.
type OwnWAD struct {
	Name  string `json:"name"`
	Path  string `json:"path,omitempty"`
	Bytes int64  `json:"bytes,omitempty"`
	Found bool   `json:"found"`
}

// FindOwnWADs looks each name up in dir: the exact file name first, then the
// same name in another case (a folder copied from Windows keeps METAL.WAD).
// Nothing below dir is searched: a folder is named so that what is used is
// predictable.
func FindOwnWADs(dir string, names []string) ([]OwnWAD, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, errors.New("playrun: your own WAD folder must be an absolute path")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("playrun: your own WAD folder cannot be read: %w", err)
	}
	byLower := map[string]string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			byLower[strings.ToLower(entry.Name())] = entry.Name()
		}
	}
	out := make([]OwnWAD, 0, len(names))
	for _, name := range names {
		found := OwnWAD{Name: name}
		actual := name
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			actual = byLower[strings.ToLower(name)]
		}
		if actual != "" {
			if info, err := os.Stat(filepath.Join(dir, actual)); err == nil && info.Mode().IsRegular() {
				found.Path, found.Bytes, found.Found = filepath.Join(dir, actual), info.Size(), true
			}
		}
		out = append(out, found)
	}

	return out, nil
}

// completeWithOwnWADs makes this run's content root: the verified bundle's
// files, plus the person's own copy of each WAD the bundle could not carry,
// under the name the map declares. The bundle's own directory is never
// written to — it is a cache entry other runs share.
func (s *Service) completeWithOwnWADs(record *Record, bundleRoot string, names []string) (string, []StagedFile, error) {
	found, err := FindOwnWADs(record.Request.OwnWADsDir, names)
	if err != nil {
		return "", nil, err
	}
	var missing []string
	for _, wad := range found {
		if !wad.Found {
			missing = append(missing, wad.Name)
		}
	}
	if len(missing) > 0 {
		return "", nil, fmt.Errorf("your folder has no %s", strings.Join(missing, ", "))
	}

	root := filepath.Join(s.store.Root(), record.ID+".textures")
	if err := os.RemoveAll(root); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", nil, err
	}
	// The bundle first, file for file.
	err = filepath.WalkDir(bundleRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(bundleRoot, path)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(root, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		_, _, err = copyFile(path, target)

		return err
	})
	if err != nil {
		return "", nil, fmt.Errorf("playrun: copying the texture bundle: %w", err)
	}
	// Then the person's own copies, recorded.
	staged := make([]StagedFile, 0, len(found))
	for _, wad := range found {
		digest, size, err := copyFile(wad.Path, filepath.Join(root, wad.Name))
		if err != nil {
			return "", nil, fmt.Errorf("playrun: copying your %s: %w", wad.Name, err)
		}
		staged = append(staged, StagedFile{Path: wad.Name, SHA256: digest, Bytes: size})
	}

	return root, staged, nil
}

func copyFile(from, to string) (string, int64, error) {
	source, err := os.Open(from)
	if err != nil {
		return "", 0, err
	}
	defer source.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return "", 0, err
	}
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(target, hash), source)
	if closeErr := target.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", 0, err
	}

	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// uniqueStrings drops repeats from a sorted list.
func uniqueStrings(sorted []string) []string {
	out := make([]string, 0, len(sorted))
	for _, value := range sorted {
		if len(out) > 0 && out[len(out)-1] == value {
			continue
		}
		out = append(out, value)
	}

	return out
}
