package aue

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// PinSchemaVersion versions the local record of which extractor build this
// machine last verified a requirement for.
const PinSchemaVersion = "aucom.extractor-pin/1.0"

// Pin is what this machine remembers about the compatibility manifest.
//
// # Why it exists
//
// Offline, there is no compatibility manifest to read, and the version and the
// minimum protocol it named are the two facts a resolution cannot do without.
// Without this record an offline Companion would have to either skip the
// protocol check — weakening a gate to make a convenience work — or refuse to
// run an extractor it already has, which is the security theatre
// `internal/acquire`'s package comment argues against at length.
//
// So the requirement is RECORDED when it is verified, and offline resolution
// uses the recorded one. That is the same trade the install record makes: an
// entry carries the facts that were verified when it was installed, and using
// it later needs no catalogue.
//
// # It is not a cache and it is not authority
//
// It is written only after a signed manifest verified, it is used only when the
// Companion was told not to use the network, and it is keyed on the exact
// question it answers — this component, this Companion version, this platform —
// so a pin written for one build is never applied to another. A pin that does
// not match is ignored rather than adapted.
//
// It lives beside `config.json` and the catalogue state rather than in the
// cache, for the reason [catalog.State] gives: it records a decision, and
// clearing a cache must not erase one.
type Pin struct {
	SchemaVersion string `json:"schema_version"`

	Component        string           `json:"component"`
	CompanionVersion string           `json:"companion_version"`
	Platform         profile.Platform `json:"platform"`

	// Version and MinProtocol are the requirement itself.
	Version     string `json:"version"`
	MinProtocol string `json:"min_protocol"`

	// Which published state of the world said so. Recorded so a person reading
	// this file can tell how old the answer is, and so a support conversation
	// about "why is it still installing 1.170" has a serial to look at.
	CompatibilityID     string    `json:"compatibility_id,omitempty"`
	CompatibilitySerial int64     `json:"compatibility_serial,omitempty"`
	ResolvedAt          time.Time `json:"resolved_at"`
}

// Matches reports whether this pin answers the question being asked.
func (p *Pin) Matches(component, companionVersion string, platform profile.Platform) bool {
	return p != nil &&
		p.Component == component &&
		p.CompanionVersion == companionVersion &&
		p.Platform == platform &&
		p.Version != "" &&
		p.MinProtocol != ""
}

// Requirement renders the pin as the requirement it recorded.
func (p *Pin) Requirement() catalog.Requirement {
	return catalog.Requirement{
		MinCompanion: p.CompanionVersion,
		Version:      p.Version,
		MinProtocol:  p.MinProtocol,
	}
}

// LoadPin reads the pin file. An absent file is `(nil, nil)`: a machine that
// has never resolved a requirement online is an ordinary state and not a fault.
func LoadPin(path string) (*Pin, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("aue: reading the extractor pin: %w", err)
	}
	var pin Pin
	if err := json.Unmarshal(raw, &pin); err != nil {
		return nil, fmt.Errorf("aue: reading the extractor pin: %w", err)
	}
	if pin.SchemaVersion != PinSchemaVersion {
		// Ignored rather than an error: a pin this build cannot read is a pin
		// it should not act on, and the next online resolution replaces it.
		return nil, nil
	}

	return &pin, nil
}

// SavePin writes the pin atomically.
func SavePin(path string, pin *Pin) error {
	if path == "" || pin == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("aue: writing the extractor pin: %w", err)
	}
	encoded, err := json.MarshalIndent(pin, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
		return fmt.Errorf("aue: writing the extractor pin: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		os.Remove(temporary)

		return fmt.Errorf("aue: writing the extractor pin: %w", err)
	}

	return nil
}
