package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrHomepageUnchanged is a homepage edit that would change nothing.
var ErrHomepageUnchanged = errors.New("profile: the homepage is already that")

// WithHomepage returns the document with `source.homepage` set — or removed,
// for "" — and its patch version bumped.
//
// The version moves because a document's bytes may not change under the same
// version ([CheckVersionImmutable]); the result is a new document with a new
// digest, so an approval of the old one does not carry over, and whoever
// approved it approves it again. Nothing about what it may run changes, and
// the review says so.
//
// The edit is made on the canonical tree and decoded again, so the URL goes
// through the same validation a typed document does: only https, no
// credentials, nothing local.
func WithHomepage(p Profile, homepage string) (Profile, error) {
	homepage = strings.TrimSpace(homepage)
	current := ""
	if source := p.Metadata().Source; source != nil {
		current = source.Homepage
	}
	if current == homepage {
		return nil, ErrHomepageUnchanged
	}
	canonical, err := Export(p)
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	if err := json.Unmarshal(canonical, &tree); err != nil {
		return nil, err
	}
	source, _ := tree["source"].(map[string]any)
	if source == nil {
		source = map[string]any{}
	}
	if homepage == "" {
		delete(source, "homepage")
	} else {
		source["homepage"] = homepage
	}
	if len(source) == 0 {
		delete(tree, "source")
	} else {
		tree["source"] = source
	}
	version, err := ParseVersion(p.Metadata().Version)
	if err != nil {
		return nil, err
	}
	tree["version"] = Version{Major: version.Major, Minor: version.Minor, Patch: version.Patch + 1}.String()

	encoded, err := json.Marshal(tree)
	if err != nil {
		return nil, err
	}
	edited, err := Decode(encoded)
	if err != nil {
		return nil, fmt.Errorf("profile: that homepage cannot be used: %w", err)
	}

	return edited, nil
}

// WriteCanonical writes a document's canonical bytes to path atomically: what
// lands is what its digest covers, and a crash mid-write leaves the previous
// document rather than half of a new one.
func WriteCanonical(path string, p Profile) error {
	canonical, err := Export(p)
	if err != nil {
		return err
	}
	temporary := path + ".writing"
	if err := os.WriteFile(temporary, append(canonical, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		os.Remove(temporary)
		return err
	}

	return nil
}
