package playrun

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// A texture WAD must be where the MAP says it is.
//
// AUB names a map's WADs by file name (`wads_declared: ["metal.wad"]`), and the
// content root is laid out that way. The converted `.map` keeps the author's
// declaration — dm2's worldspawn says `"wad" "gfx/metal.wad"` — and a compiler
// that joins `-wadpath` with the DECLARED path finds nothing at the root: ericw-
// tools 2.0.0-alpha11 compiled dm2 with all 38 textures missing and exited zero
// (operator, 2026-09-23: "the map in vQuake is missing the textures"). 0.18
// also tries the bare name, which is why the built-in profile never showed it.
//
// So, after conversion and before the compile, every WAD the map declares under
// a directory is also placed at that path, copied from the file of the same name
// the content root already holds. The shared bundle cache is never written to:
// the run gets a content root of its own first.

// declaredWADPattern is worldspawn's `wad` key. Worldspawn is the first entity
// and its keys come before any brush, so the first match is its declaration.
var declaredWADPattern = regexp.MustCompile(`(?i)"wad"\s+"([^"]*)"`)

// maxWorldspawnScan bounds how much of a map is read to find the key.
const maxWorldspawnScan = 1 << 20

// DeclaredWADs is the WAD list a `.map`'s worldspawn declares, in order,
// separators normalised to `/`.
func DeclaredWADs(mapPath string) ([]string, error) {
	file, err := os.Open(mapPath)
	if errors.Is(err, fs.ErrNotExist) {
		// Nothing to place. Whether the map exists is the compile's question,
		// and it answers it in its own words.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	head, err := io.ReadAll(io.LimitReader(file, maxWorldspawnScan))
	if err != nil {
		return nil, err
	}
	match := declaredWADPattern.FindSubmatch(head)
	if match == nil {
		return nil, nil
	}
	var out []string
	for _, entry := range strings.Split(string(match[1]), ";") {
		if entry = strings.TrimSpace(strings.ReplaceAll(entry, `\`, "/")); entry != "" {
			out = append(out, entry)
		}
	}

	return out, nil
}

// declaredRelative is where under the content root a declared WAD must be, or
// "" when the declaration is just a file name, or is absolute, has a drive, or
// climbs out with `..` — none of which is a place under the root.
func declaredRelative(declared string) string {
	if declared == "" || strings.HasPrefix(declared, "/") || strings.Contains(declared, ":") {
		return ""
	}
	clean := path.Clean(declared)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || !strings.Contains(clean, "/") {
		return ""
	}

	return clean
}

// placeDeclaredWADs puts each WAD the converted map declares under a directory
// at that path in the run's content root. It returns the relative paths it
// placed; nothing is placed, and the root left as it is, when the map declares
// only file names or every declared path is already there.
func (s *Service) placeDeclaredWADs(record *Record, mapPath string) ([]string, error) {
	declared, err := DeclaredWADs(mapPath)
	if err != nil || len(declared) == 0 || record.BundleRoot == "" {
		return nil, err
	}
	names, err := rootFileNames(record.BundleRoot)
	if err != nil {
		return nil, err
	}
	type placement struct{ from, to string }
	var needed []placement
	for _, entry := range declared {
		relative := declaredRelative(entry)
		if relative == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(record.BundleRoot, filepath.FromSlash(relative))); err == nil {
			continue
		}
		if actual, ok := names[strings.ToLower(path.Base(relative))]; ok {
			needed = append(needed, placement{from: actual, to: relative})
		}
	}
	if len(needed) == 0 {
		return nil, nil
	}

	root := filepath.Join(s.store.Root(), record.ID+".textures")
	if filepath.Clean(record.BundleRoot) != filepath.Clean(root) {
		// The bundle is a cache entry other runs share: this run gets a copy.
		if err := copyTree(record.BundleRoot, root); err != nil {
			return nil, err
		}
		record.BundleRoot = root
	}
	placed := make([]string, 0, len(needed))
	for _, p := range needed {
		if _, _, err := copyFile(filepath.Join(root, p.from), filepath.Join(root, filepath.FromSlash(p.to))); err != nil {
			return nil, err
		}
		placed = append(placed, p.to)
	}

	return placed, nil
}

// rootFileNames maps each lower-cased file name at the top of dir to its actual
// name.
func rootFileNames(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			out[strings.ToLower(entry.Name())] = entry.Name()
		}
	}

	return out, nil
}

// copyTree copies every regular file under from to the same place under to,
// replacing whatever to held.
func copyTree(from, to string) error {
	if err := os.RemoveAll(to); err != nil {
		return err
	}
	return filepath.WalkDir(from, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(from, current)
		if err != nil {
			return err
		}
		target := filepath.Join(to, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		_, _, err = copyFile(current, target)

		return err
	})
}
