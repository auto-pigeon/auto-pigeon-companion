package managedtools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/lockfile"
)

// InstallSchema versions install.json.
const InstallSchema = "aucom.managed-tool-install/1"

// InstallRecordName is the record inside each installation.
const InstallRecordName = "install.json"

// InstallRecord is everything that ties a later job to these exact bytes.
type InstallRecord struct {
	Schema     string  `json:"schema"`
	Tool       ToolID  `json:"tool"`
	Repository string  `json:"repository"`
	Tag        string  `json:"tag"`
	Version    string  `json:"version"`
	Banner     string  `json:"version_banner"`
	Commit     string  `json:"source_commit"`
	ReleaseID  int64   `json:"release_id"`
	NotesURL   string  `json:"release_notes"`
	Prerelease bool    `json:"prerelease"`
	Channel    Channel `json:"channel"`
	Target     string  `json:"target"`
	Asset      string  `json:"asset"`
	AssetID    int64   `json:"asset_id"`
	Size       int64   `json:"size"`
	// ExpectedSHA256 is the release manifest's; ActualSHA256 what the
	// downloaded bytes hashed to. Equal, or there is no record.
	ExpectedSHA256 string `json:"expected_sha256"`
	ActualSHA256   string `json:"actual_sha256"`
	GitHubDigest   string `json:"github_digest,omitempty"`
	ManifestSHA256 string `json:"release_manifest_sha256"`
	Qualification  string `json:"qualification"`
	// Signed is false: a digest in the same release shows integrity, not
	// who published it.
	Signed bool `json:"signed"`
	// Dir is the installation's root; Programs its programs' absolute paths
	// by name (qbsp, vis, q3map2).
	Dir      string            `json:"dir"`
	Programs map[string]string `json:"programs"`
	// Source and Licenses say where the corresponding source and the
	// licence texts are, as the release ships them.
	Source      string    `json:"corresponding_source"`
	Licenses    string    `json:"licenses"`
	CompletedAt time.Time `json:"completed_at"`
	// Members is a Companion bundle's every file with its digest, relative
	// to Dir: what replacing the running bundle copies and checks again.
	Members       []BundleMember `json:"members,omitempty"`
	Sidecar       *Sidecar       `json:"sidecar,omitempty"`
	PlatformState string         `json:"platform_state,omitempty"`
}

// Ref is the record's index entry.
func (r InstallRecord) Ref() InstallRef {
	return InstallRef{Version: r.Version, Tag: r.Tag, AssetID: r.AssetID, SHA256: r.ActualSHA256, Dir: r.Dir, CompletedAt: r.CompletedAt}
}

// ErrInstallBusy is another Companion installing this tool right now.
var ErrInstallBusy = errors.New("managedtools: another Auto-Pigeon Companion is installing this tool")

// ErrNoSpace is a disk without room for the download, its unpacked files
// and the margin a running build needs.
var ErrNoSpace = errors.New("managedtools: not enough free disk space")

// freeMargin is left free beyond what an installation needs, so installing
// a compiler never takes the space a build that is running is writing into.
const freeMargin = 512 << 20

// Progress reports how far an install has come.
type Progress func(phase string, done, total int64)

// installer holds what an installation needs.
type installer struct {
	root    string
	source  Source
	limits  Limits
	channel Channel
	// freeSpace is how many bytes may be written at a path; -1 is unknown.
	freeSpace func(string) int64
}

// dirName is an installation's folder: short, so a Windows path stays well
// inside MAX_PATH, and naming the bytes as well as the version.
func dirName(candidate Candidate) string {
	return candidate.Version + "-" + candidate.SHA256[:8]
}

func (in installer) toolDir(tool Tool) string { return filepath.Join(in.root, string(tool.ID)) }

// install makes one verified installation of a candidate, or finds the one
// that is already there. It never touches another installation.
func (in installer) install(ctx context.Context, tool Tool, candidate Candidate, progress Progress) (InstallRecord, error) {
	if progress == nil {
		progress = func(string, int64, int64) {}
	}
	toolDir := in.toolDir(tool)
	final := filepath.Join(toolDir, dirName(candidate))
	if record, err := ReadInstall(final); err == nil && record.ActualSHA256 == candidate.SHA256 && record.AssetID == candidate.AssetID {
		return record, nil
	}
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		return InstallRecord{}, fmt.Errorf("managedtools: the tools folder %s cannot be created: %w", toolDir, err)
	}
	lock, err := lockfile.Acquire(filepath.Join(toolDir, "install"), lockfile.Options{
		Timeout: 2 * time.Second, Program: "auto-pigeon-companion installing " + string(tool.ID)})
	if errors.Is(err, lockfile.ErrBusy) {
		return InstallRecord{}, ErrInstallBusy
	}
	if err != nil {
		return InstallRecord{}, fmt.Errorf("managedtools: %s is not writable: %w", toolDir, err)
	}
	defer lock.Release()
	// Under the lock, so another process may have finished it meanwhile.
	if record, err := ReadInstall(final); err == nil && record.ActualSHA256 == candidate.SHA256 && record.AssetID == candidate.AssetID {
		return record, nil
	}
	// What an earlier, interrupted attempt left: a staging folder, or a final
	// folder with no record (a rename cannot leave one, a copy by hand can).
	// Both are this program's own, and neither is anything's installation.
	staging := filepath.Join(toolDir, ".staging")
	_ = os.RemoveAll(staging)
	if _, err := os.Stat(final); err == nil {
		if err := os.RemoveAll(final); err != nil {
			return InstallRecord{}, err
		}
	}
	if candidate.Size <= 0 || candidate.Size > in.limits.MaxCompressed {
		return InstallRecord{}, refuse("the release says its archive is %d bytes; at most %d are accepted", candidate.Size, in.limits.MaxCompressed)
	}
	// The download, its unpacked files (an archive is about a third of
	// them) and the margin.
	needed := candidate.Size + 4*candidate.Size + freeMargin
	if free := in.free(toolDir); free >= 0 && free < needed {
		return InstallRecord{}, fmt.Errorf("%w: %s needs about %d MB in %s and %d MB are free",
			ErrNoSpace, candidate.Asset, needed>>20, toolDir, free>>20)
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return InstallRecord{}, err
	}
	work, err := os.MkdirTemp(staging, "dl-")
	if err != nil {
		return InstallRecord{}, err
	}
	defer os.RemoveAll(work)

	archivePath := filepath.Join(work, "archive.zip")
	file, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return InstallRecord{}, err
	}
	hash := sha256.New()
	counter := &progressWriter{report: func(n int64) { progress("downloading", n, candidate.Size) }}
	progress("downloading", 0, candidate.Size)
	written, downloadErr := in.source.Download(ctx, tool, candidate.Tag, candidate.Asset, candidate.Size, io.MultiWriter(file, hash, counter))
	closeErr := file.Close()
	switch {
	case downloadErr != nil:
		return InstallRecord{}, fmt.Errorf("managedtools: downloading %s: %w", candidate.Asset, downloadErr)
	case closeErr != nil:
		return InstallRecord{}, closeErr
	case written != candidate.Size:
		return InstallRecord{}, refuse("%s arrived as %d bytes; the release says %d (the download was cut short)", candidate.Asset, written, candidate.Size)
	}
	progress("verifying", written, candidate.Size)
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != candidate.SHA256 {
		return InstallRecord{}, refuse("%s is not the file the release describes: its SHA-256 is %s and the release says %s", candidate.Asset, actual, candidate.SHA256)
	}
	if github, ok := strings.CutPrefix(candidate.GitHubDigest, "sha256:"); ok && github != actual {
		return InstallRecord{}, refuse("%s does not match GitHub's own digest of it", candidate.Asset)
	}
	unpacked := filepath.Join(work, "root")
	if err := os.Mkdir(unpacked, 0o755); err != nil {
		return InstallRecord{}, err
	}
	programs := map[string]string{}
	var members []BundleMember
	commit, licenses := "", filepath.Join(final, "licenses")
	if tool.Kind == KindApplication {
		bundle, err := unpackBundle(archivePath, unpacked, candidate, in.limits)
		if err != nil {
			return InstallRecord{}, err
		}
		for name, relative := range bundle.programs {
			inStaging := filepath.Join(unpacked, filepath.FromSlash(relative))
			if err := checkProgram(inStaging, candidate.Target); err != nil {
				return InstallRecord{}, err
			}
			if err := checkArchitecture(inStaging, candidate.Target); err != nil {
				return InstallRecord{}, err
			}
			programs[name] = filepath.Join(final, filepath.FromSlash(relative))
		}
		members, licenses = bundle.members, ""
	} else {
		manifest, err := extract(archivePath, unpacked, tool, candidate, in.limits)
		if err != nil {
			return InstallRecord{}, err
		}
		commit = manifest.Commit
		for _, program := range tool.Programs {
			name := filepath.Base(program)
			inStaging := programPath(unpacked, program, candidate.Target)
			if err := checkProgram(inStaging, candidate.Target); err != nil {
				return InstallRecord{}, err
			}
			if err := checkArchitecture(inStaging, candidate.Target); err != nil {
				return InstallRecord{}, err
			}
			programs[name] = programPath(final, program, candidate.Target)
		}
	}
	record := InstallRecord{
		Schema:         InstallSchema,
		Tool:           tool.ID,
		Repository:     tool.Owner + "/" + tool.Repo,
		Tag:            candidate.Tag,
		Version:        candidate.Version,
		Banner:         candidate.Banner,
		Commit:         firstNonEmpty(candidate.Commit, commit),
		ReleaseID:      candidate.ReleaseID,
		NotesURL:       candidate.NotesURL,
		Prerelease:     candidate.Prerelease,
		Channel:        in.channel,
		Target:         candidate.Target,
		Asset:          candidate.Asset,
		AssetID:        candidate.AssetID,
		Size:           candidate.Size,
		ExpectedSHA256: candidate.SHA256,
		ActualSHA256:   actual,
		GitHubDigest:   candidate.GitHubDigest,
		ManifestSHA256: candidate.ManifestSHA256,
		Qualification:  candidate.Qualification,
		Signed:         false,
		Dir:            final,
		Programs:       programs,
		Source:         "https://github.com/" + tool.Owner + "/" + tool.Repo + "/tree/" + candidate.Tag,
		Licenses:       licenses,
		CompletedAt:    time.Now().UTC(),
		Members:        members,
		Sidecar:        candidate.Sidecar,
		PlatformState:  candidate.PlatformState,
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return InstallRecord{}, err
	}
	if err := os.WriteFile(filepath.Join(unpacked, InstallRecordName), append(encoded, '\n'), 0o644); err != nil {
		return InstallRecord{}, err
	}
	// The one step that makes it an installation, and it is a rename: there
	// is never a final folder half-written.
	if err := os.Rename(unpacked, final); err != nil {
		return InstallRecord{}, fmt.Errorf("managedtools: completing %s: %w", final, err)
	}
	progress("installed", written, candidate.Size)
	return record, nil
}

func (in installer) free(dir string) int64 {
	if in.freeSpace != nil {
		return in.freeSpace(dir)
	}
	return freeBytes(dir)
}

// ReadInstall reads and checks one installation's record: the record is
// complete, it is in the folder it names, and every program it lists is a
// regular, runnable file there. It does not re-hash the archive, which is
// gone; the version check that follows asks the programs themselves.
func ReadInstall(dir string) (InstallRecord, error) {
	raw, err := os.ReadFile(filepath.Join(dir, InstallRecordName))
	if err != nil {
		return InstallRecord{}, err
	}
	var record InstallRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return InstallRecord{}, fmt.Errorf("managedtools: %s is not readable: %w", filepath.Join(dir, InstallRecordName), err)
	}
	absolute, _ := filepath.Abs(dir)
	switch {
	case record.Schema != InstallSchema:
		return InstallRecord{}, fmt.Errorf("managedtools: %s is %q", dir, record.Schema)
	case filepath.Clean(record.Dir) != filepath.Clean(absolute):
		return InstallRecord{}, fmt.Errorf("managedtools: the record in %s describes %s", dir, record.Dir)
	case record.ActualSHA256 == "" || record.ActualSHA256 != record.ExpectedSHA256:
		return InstallRecord{}, fmt.Errorf("managedtools: the record in %s does not show matching digests", dir)
	case len(record.Programs) == 0:
		return InstallRecord{}, fmt.Errorf("managedtools: the record in %s lists no programs", dir)
	}
	for name, program := range record.Programs {
		if !within(absolute, program) {
			return InstallRecord{}, fmt.Errorf("managedtools: %s's %s is outside it", dir, name)
		}
		if err := checkProgram(program, record.Target); err != nil {
			return InstallRecord{}, err
		}
	}
	return record, nil
}

func checkProgram(path, target string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return refuse("%s is not there", path)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return refuse("%s is not a regular file", path)
	}
	if exeSuffix(target) == "" && info.Mode().Perm()&0o111 == 0 {
		return refuse("%s is not executable", path)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type progressWriter struct {
	n      int64
	last   time.Time
	report func(int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	if now := time.Now(); now.Sub(p.last) > 250*time.Millisecond {
		p.last = now
		p.report(p.n)
	}
	return len(b), nil
}
