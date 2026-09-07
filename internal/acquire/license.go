package acquire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
)

// AcceptanceSchemaVersion versions the local licence-acknowledgement file.
const AcceptanceSchemaVersion = "aucom.license-acceptance/1.0"

// What this file is, and — more importantly — what it is not.
//
// Some of the programs the Companion can download come with a licence that
// requires a notice be shown before the program is obtained. This records that
// the notice was shown and that the person said they had read it.
//
// It is a local note. It is not a licence, it is not a grant, it does not come
// from Auto-Pigeon, and it does not change anybody's obligations under the
// licence it is about — clicking a button in this program has no bearing on
// what the GPL requires of anyone. The record exists so that the Companion can
// tell "this person has been shown the notice" from "this person has not", and
// so that it does not show the same wall of text before every build.
//
// The notice's digest is part of the record on purpose. If the licence text
// changes, the acceptance no longer covers what the user would now be shown,
// and they are shown it again. An acceptance keyed only by package and version
// would silently carry forward across a text a person never read.

// Acceptance is one recorded acknowledgement.
type Acceptance struct {
	PackageID string `json:"package_id"`
	Version   string `json:"version"`
	SPDX      string `json:"spdx"`
	// NoticeDigest is the SHA-256 of the exact text that was shown.
	NoticeDigest string    `json:"notice_digest"`
	AcceptedAt   time.Time `json:"accepted_at"`
}

// Acceptances is the whole local file.
type Acceptances struct {
	SchemaVersion string       `json:"schema_version"`
	Accepted      []Acceptance `json:"accepted,omitempty"`
}

// NoticeDigest is the digest recorded for a package's notice. A package that
// requires acceptance and carries no notice text still gets a stable digest,
// over its identifier, so that the record is keyed by something.
func NoticeDigest(pkg catalog.Package) string {
	text := pkg.License.Notice
	if strings.TrimSpace(text) == "" {
		text = pkg.License.SPDX + "\n" + pkg.License.URL
	}
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Covers reports whether a package's notice has already been acknowledged.
func (a *Acceptances) Covers(pkg catalog.Package) bool {
	if a == nil {
		return false
	}
	digest := NoticeDigest(pkg)
	for _, accepted := range a.Accepted {
		if accepted.PackageID == pkg.ID && accepted.Version == pkg.Version && accepted.NoticeDigest == digest {
			return true
		}
	}
	return false
}

// Record adds an acknowledgement, replacing any earlier one for the same
// package and version.
func (a *Acceptances) Record(pkg catalog.Package, now time.Time) {
	entry := Acceptance{
		PackageID:    pkg.ID,
		Version:      pkg.Version,
		SPDX:         pkg.License.SPDX,
		NoticeDigest: NoticeDigest(pkg),
		AcceptedAt:   now.UTC(),
	}
	for i := range a.Accepted {
		if a.Accepted[i].PackageID == pkg.ID && a.Accepted[i].Version == pkg.Version {
			a.Accepted[i] = entry
			return
		}
	}
	a.Accepted = append(a.Accepted, entry)
	sort.Slice(a.Accepted, func(i, j int) bool {
		if a.Accepted[i].PackageID == a.Accepted[j].PackageID {
			return a.Accepted[i].Version < a.Accepted[j].Version
		}
		return a.Accepted[i].PackageID < a.Accepted[j].PackageID
	})
}

// LoadAcceptances reads the local file. A missing file is an empty set.
func LoadAcceptances(path string) (*Acceptances, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Acceptances{SchemaVersion: AcceptanceSchemaVersion}, nil
		}
		return nil, fmt.Errorf("acquire: reading %s: %w", path, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var accepted Acceptances
	if err := decoder.Decode(&accepted); err != nil {
		return nil, fmt.Errorf("acquire: reading %s: %w", path, err)
	}
	if accepted.SchemaVersion != AcceptanceSchemaVersion {
		return nil, fmt.Errorf("acquire: %s is %q; this build reads %q", path, accepted.SchemaVersion, AcceptanceSchemaVersion)
	}
	return &accepted, nil
}

// SaveAcceptances writes the local file atomically.
func SaveAcceptances(path string, accepted *Acceptances) error {
	accepted.SchemaVersion = AcceptanceSchemaVersion
	encoded, err := json.MarshalIndent(accepted, "", "  ")
	if err != nil {
		return fmt.Errorf("acquire: encoding the licence acknowledgements: %w", err)
	}
	encoded = append(encoded, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("acquire: creating %s: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, "license-acceptance-*.json")
	if err != nil {
		return fmt.Errorf("acquire: creating a temporary file in %s: %w", dir, err)
	}
	name := temp.Name()
	defer os.Remove(name) // No-op once the rename below has succeeded.

	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("acquire: securing %s: %w", name, err)
	}
	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return fmt.Errorf("acquire: writing %s: %w", name, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("acquire: closing %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("acquire: replacing %s: %w", path, err)
	}
	return nil
}
