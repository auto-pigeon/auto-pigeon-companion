package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Reading somebody else's archive.
//
// Inspection and verification are separate operations, and the split is the
// point rather than a convenience. [Inspect] reads the directory and nothing
// else: it is bounded by the size of the directory, it never decompresses, and
// it is what a person runs on a file they do not trust. [Verify] reads every
// member, which means spending the archive's whole declared budget, and is what
// a person runs once inspection has said the declaration is plausible.
//
// An archive that fails inspection is never verified. That ordering is what
// makes the bomb checks worth having.

// Inspection is what an archive's directory says about itself.
type Inspection struct {
	Path   string `json:"path"`
	Format Format `json:"format"`
	// Size is the container's own length; SHA256 its digest. An archive is a
	// published artifact, so the first question about one is which one it is.
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// TotalSize is what the members declare they expand to.
	TotalSize int64   `json:"total_size"`
	Entries   []Entry `json:"entries"`
	// Collisions are the member paths a reader would confuse for one file.
	// Reported rather than refused: this archive already exists, and the useful
	// thing to do with one is to say what is wrong with it.
	Collisions []Collision `json:"collisions,omitempty"`
	// Notes are structural observations that are not faults.
	Notes []string `json:"notes,omitempty"`
}

// Ratio is how much larger the declared contents are than the container.
func (i Inspection) Ratio() float64 {
	if i.Size == 0 {
		return 0
	}
	return float64(i.TotalSize) / float64(i.Size)
}

// Inspect reads an archive's directory.
//
// The format is taken from `format` when the caller knows it, and guessed from
// the extension when it does not. A wrong guess produces the format's own
// refusal — "it starts with \"PK\\x03\\x04\", not \"PACK\"" — which is a better
// message than anything this function could construct, so it does not try.
func Inspect(path string, format Format, budget Budget) (*Inspection, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("pack: opening %s: %w", path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("pack: reading %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("pack: %s is not a regular file", path)
	}
	if format == "" {
		guessed, ok := FormatOf(filepath.Base(path))
		if !ok {
			return nil, fmt.Errorf("pack: %s does not end in .pak, .pk3 or .zip; say which format it is", path)
		}
		format = guessed
	}

	var (
		entries []Entry
		notes   []string
	)
	switch format {
	case FormatPAK:
		entries, notes, err = ReadPAK(file, info.Size(), budget)
	case FormatPK3:
		entries, notes, err = ReadPK3(file, info.Size(), budget)
	default:
		return nil, fmt.Errorf("pack: %q is not a format this build reads", format)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	digest, err := fileDigest(path)
	if err != nil {
		return nil, err
	}
	inspection := &Inspection{
		Path: path, Format: format, Size: info.Size(), SHA256: digest,
		Entries: entries, Notes: notes,
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		inspection.TotalSize += entry.Size
		paths = append(paths, entry.Path)
	}
	inspection.Collisions = FindCollisions(paths)
	return inspection, nil
}

// Verification is the result of reading every member.
type Verification struct {
	Inspection
	// Verified is how many members were read whole and matched everything the
	// directory said about them.
	Verified int `json:"verified"`
	// Problems is every member that did not, one line each. A slice rather
	// than the first error, because "17 members are truncated" is a different
	// diagnosis from "one member is truncated" and a caller that stopped at the
	// first could not tell them apart.
	Problems []string `json:"problems,omitempty"`
	// ManifestPath and ManifestAgrees are set when a sidecar manifest was found
	// beside the archive and compared against it.
	ManifestPath   string   `json:"manifest_path,omitempty"`
	ManifestAgrees bool     `json:"manifest_agrees,omitempty"`
	ManifestIssues []string `json:"manifest_issues,omitempty"`
}

// OK reports an archive that read cleanly and, if it had a manifest, agreed
// with it.
func (v Verification) OK() bool {
	return len(v.Problems) == 0 && (v.ManifestPath == "" || v.ManifestAgrees)
}

// Verify reads every member and checks it against what the directory declared.
//
// It fills in each entry's SHA-256, which inspection deliberately leaves empty:
// a digest costs a full read of the member, and the whole reason inspection
// exists is to be the cheap thing that runs first.
func Verify(path string, format Format, budget Budget) (*Verification, error) {
	inspection, err := Inspect(path, format, budget)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("pack: opening %s: %w", path, err)
	}
	defer file.Close()

	result := &Verification{Inspection: *inspection}
	remaining := budget.maxTotalSize()

	switch inspection.Format {
	case FormatPAK:
		for i, entry := range result.Entries {
			hash := sha256.New()
			written, err := io.Copy(hash, io.LimitReader(openPAKMember(file, entry), entry.Size+1))
			switch {
			case err != nil:
				result.Problems = append(result.Problems, fmt.Sprintf("%s: %v", entry.Path, err))
				continue
			case written != entry.Size:
				result.Problems = append(result.Problems, fmt.Sprintf(
					"%s: the directory says %d bytes and the file holds %d", entry.Path, entry.Size, written))
				continue
			}
			remaining -= written
			result.Entries[i].SHA256 = "sha256:" + hex.EncodeToString(hash.Sum(nil))
			result.Verified++
		}
	case FormatPK3:
		reader, err := openPK3(file, inspection.Size)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		byPath := map[string]int{}
		for i, entry := range result.Entries {
			byPath[entry.Path] = i
		}
		for _, member := range reader.File {
			index, wanted := byPath[member.Name]
			if !wanted {
				continue
			}
			written, digest, err := verifyPK3Member(member, remaining)
			if err != nil {
				result.Problems = append(result.Problems, err.Error())
				continue
			}
			remaining -= written
			result.Entries[index].SHA256 = digest
			result.Verified++
		}
	}

	if manifest, manifestPath, err := findManifest(path); err == nil && manifest != nil {
		result.ManifestPath = manifestPath
		result.ManifestIssues = manifest.Disagreements(&result.Inspection)
		result.ManifestAgrees = len(result.ManifestIssues) == 0
	}
	return result, nil
}

// fileDigest is the SHA-256 of a whole file, in this program's usual spelling.
func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("pack: opening %s: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("pack: reading %s: %w", path, err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// Summary is the one line `companion package inspect` prints above the listing.
func (i Inspection) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  %d members, %s stored, %s of contents",
		filepath.Base(i.Path), i.Format, len(i.Entries), humanSize(i.Size), humanSize(i.TotalSize))
	if ratio := i.Ratio(); ratio >= 2 {
		fmt.Fprintf(&b, " (%.1fx)", ratio)
	}
	return b.String()
}

// humanSize is for reading, never for comparing. Every place a size is checked
// uses the integer.
func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
