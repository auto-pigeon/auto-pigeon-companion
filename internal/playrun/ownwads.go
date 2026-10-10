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

// Your own copy of a WAD the deployment did not send.
//
// A map names its texture WADs, and an Auto-Pigeon deployment sends the bytes
// of a WAD it has installed only when its operator has a redistribution
// permission on record for those exact bytes (`NEW_313A`). When it has none,
// the bundle arrives without the file and AUB says the map is not
// compiler-ready (`wad_bytes_not_carried`). That is the whole of what is known
// here: nothing on record permits handing those bytes out. It is NOT a
// statement about whose work the file is or where a copy of it comes from, and
// nothing in this program may say that it is.
//
// The remedy is local: the person supplies their own copy of the file.
//
// The rule that did not change: the Companion never QUIETLY uses a similarly
// named WAD from a folder on the person's machine. Here the person names a
// folder, in the review, for this run; only the WADs AUB did not carry are
// taken from it, each by its exact file name; and every one is recorded — name,
// SHA-256, size — in the run and in the build manifest, beside the bundle it
// completed. A refusal of any other kind (a private source, a texture nobody
// supplies) still stops the run: an own copy is an answer to "was not sent"
// and to nothing else.

// OwnWADsNeeded reports the WADs a bundle lacks only because AUB did not send
// their bytes, and whether that is the only thing wrong with it.
//
// `wad_inventory_incomplete` accompanies a WAD that was not carried — AUB
// cannot list the contents of bytes it does not ship — so it does not count as
// a separate problem when at least one WAD was not carried.
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
//
// The spelling comes from the directory listing and never from probing the
// requested path: on a case-insensitive filesystem (macOS by default,
// Windows) `stat metal.wad` succeeds for METAL.WAD, and the path recorded
// would then be a spelling that is not on disk. Two regular files that differ
// only in letter case, with no exact match, are refused rather than chosen by
// listing order.
func FindOwnWADs(dir string, names []string) ([]OwnWAD, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, errors.New("playrun: your own WAD folder must be an absolute path")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("playrun: your own WAD folder cannot be read: %w", err)
	}
	listing := make([]wadEntry, 0, len(entries))
	for _, entry := range entries {
		listing = append(listing, wadEntry{name: entry.Name(), regular: entry.Type().IsRegular()})
	}
	out := make([]OwnWAD, 0, len(names))
	for _, name := range names {
		found := OwnWAD{Name: name}
		actual, err := selectOwnWAD(listing, name)
		if err != nil {
			return nil, err
		}
		if actual != "" {
			if info, err := os.Lstat(filepath.Join(dir, actual)); err == nil && info.Mode().IsRegular() {
				found.Path, found.Bytes, found.Found = filepath.Join(dir, actual), info.Size(), true
			}
		}
		out = append(out, found)
	}

	return out, nil
}

// wadEntry is one entry of the named folder, as its listing spells it.
type wadEntry struct {
	name    string
	regular bool
}

// selectOwnWAD picks the listing entry that stands for name: the regular file
// spelled exactly so, else the ONE regular file whose name differs only in
// letter case, else nothing. It never looks at the filesystem, so the rule is
// the same on a case-sensitive and a case-insensitive host.
func selectOwnWAD(listing []wadEntry, name string) (string, error) {
	var folded []string
	for _, entry := range listing {
		if !entry.regular {
			continue
		}
		if entry.name == name {
			return entry.name, nil
		}
		if strings.EqualFold(entry.name, name) {
			folded = append(folded, entry.name)
		}
	}
	switch len(folded) {
	case 0:
		return "", nil
	case 1:
		return folded[0], nil
	default:
		sort.Strings(folded)
		return "", fmt.Errorf("your folder has %d files named %s in different letter case (%s); keep one and try again",
			len(folded), name, strings.Join(folded, ", "))
	}
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

// withOwnCopies marks, in the per-WAD source list, the declarations a person's
// own copy completed: the origin becomes [OriginOwnCopy] and the digest is the
// one the file was copied at. The reason it was not sent stays.
func withOwnCopies(sources []WADSource, own []StagedFile) []WADSource {
	out := append([]WADSource(nil), sources...)
	for _, file := range own {
		matched := false
		for index := range out {
			if out[index].Staged || !strings.EqualFold(wadFileName(out[index].Name), file.Path) {
				continue
			}
			out[index].Origin, out[index].Staged = OriginOwnCopy, true
			out[index].SHA256, out[index].Bytes = file.SHA256, file.Bytes
			out[index].Revision, out[index].Source, out[index].Credit = 0, "", ""
			matched = true
		}
		if !matched {
			out = append(out, WADSource{
				Name: file.Path, Origin: OriginOwnCopy, Staged: true, SHA256: file.SHA256, Bytes: file.Bytes,
			})
		}
	}

	return out
}

// notSentReason is AUB's reason code for one declared WAD, or "" when the
// bundle did not give one — a 1.1 manifest never does.
func notSentReason(sources []WADSource, subject string) string {
	for _, source := range sources {
		if !source.Staged && (source.Name == subject ||
			strings.EqualFold(wadFileName(source.Name), wadFileName(subject))) {
			return source.NotSentReason
		}
	}

	return ""
}

// NotSentSentence is why a WAD was not sent, as a sentence, chosen by AUB's
// reason code. An unknown or absent code — every 1.1 manifest — gets the one
// sentence that is true of all of them: no redistribution permission is on
// record for that file's exact bytes. None of these says whose the file is.
func NotSentSentence(wad, reason string) string {
	switch reason {
	case "declared_withheld":
		return wad + " was not sent: the operator of this Auto-Pigeon deployment has declared that its bytes are not to be redistributed."
	case "digest_mismatch":
		return wad + " was not sent: the deployment's copy is not the exact file its redistribution permission names."
	case "declaration_incomplete":
		return wad + " was not sent: the deployment's redistribution record for it does not state a credit and terms."
	case "source_unreadable":
		return wad + " was not sent: the deployment could not read its copy of the file."
	case "policy_unavailable":
		return wad + " was not sent: the deployment's redistribution records could not be read."
	default:
		return wad + " was not sent: this Auto-Pigeon deployment has no redistribution permission on record for that file's exact bytes."
	}
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
