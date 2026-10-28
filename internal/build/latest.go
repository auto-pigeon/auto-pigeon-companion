package build

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
)

// The latest build of a map that can be played (NEW_365).
//
// One owner, for every surface that offers "Play latest build" — My Maps, Run,
// the CLI — so the button, the list and `companion build latest` cannot pick
// different builds. The build store is the record: every build writes a
// manifest (a saved-map Build & Run, a file build, an editor's leak test, an
// automatic build), and nothing here keeps a second inventory.
//
// Eligible means all of:
//
//   - of the same map: the same backend, asset type and asset id that the
//     build's own source record names. Never its display name: two maps called
//     "Arena" on two backends, or in two accounts, are two maps.
//   - a normal build. An editor's leak test (`leak_test` on the manifest) is a
//     diagnostic: its QBSP wrote a BSP, but no VIS or LIGHT was run on it as a
//     level, and it is never offered as playable however it ended.
//   - succeeded, with a known game family that matches the engine's.
//   - its level still on this machine, byte for byte what the build recorded.
//
// Latest is the newest eligible build by when it finished, then by build id —
// never by file name or directory time. A newer failed, cancelled or leak-test
// build does not displace an older playable one. The newest normal success
// whose files are missing or were changed is reported as unavailable, with
// its id, rather than quietly replaced by an older build the user did not ask
// for: a rebuild is the honest offer then.

// MapIdentity is which map a build was of, by its source record.
type MapIdentity struct {
	Backend   string `json:"backend"`
	AssetType string `json:"asset_type"`
	AssetID   string `json:"asset_id"`
}

// The states of a [Selection].
const (
	// LatestPlayable: BuildID can be played.
	LatestPlayable = "playable"
	// LatestNone: no normal build of this map succeeded on this machine.
	LatestNone = "none"
	// LatestUnavailable: the newest normal build's level is missing or changed.
	LatestUnavailable = "unavailable"
)

// Selection is the answer for one map.
type Selection struct {
	State string      `json:"state"`
	Map   MapIdentity `json:"map"`
	// The chosen build, for playable and unavailable.
	BuildID    string    `json:"build_id,omitempty"`
	Label      string    `json:"label,omitempty"`
	Pipeline   string    `json:"pipeline,omitempty"`
	Family     string    `json:"family,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	// Revision and RevisionID are the map revision the build was made from.
	Revision   int    `json:"revision,omitempty"`
	RevisionID string `json:"revision_id,omitempty"`
	MapName    string `json:"map_name,omitempty"`
	// Reason says why there is no playable build, in a sentence.
	Reason string `json:"reason,omitempty"`
	// LeakTests is how many leak tests of this map are on this machine. A map
	// with leak tests and no normal build is the case "No playable build yet"
	// exists for.
	LeakTests int `json:"leak_tests,omitempty"`
	// NewerLeakTest is the revision of a leak test newer than the chosen
	// build, when there is one: it is not included in what plays.
	NewerLeakTest int `json:"newer_leak_test_revision,omitempty"`
}

// ErrUnknownSource reports a build whose manifest names no source map.
var ErrUnknownSource = errors.New("this build does not record which saved map it was made from")

// SourceOf is the source record of a build's map: the `source_map` input's,
// else the first input with one. A build of a file on this disk has none.
func SourceOf(m *Manifest) (*SourceRef, error) {
	if m == nil {
		return nil, ErrUnknownSource
	}
	var first *SourceRef
	for _, input := range m.Inputs {
		if input.Source == nil {
			continue
		}
		if input.Name == "source_map" {
			return input.Source, nil
		}
		if first == nil {
			first = input.Source
		}
	}
	if first == nil {
		return nil, ErrUnknownSource
	}
	return first, nil
}

// IdentityOf is the map a build was of.
func IdentityOf(m *Manifest) (MapIdentity, error) {
	source, err := SourceOf(m)
	if err != nil {
		return MapIdentity{}, err
	}
	return MapIdentity{Backend: source.Backend, AssetType: source.AssetType, AssetID: source.AssetID}.normal(), nil
}

// normal compares backends as addresses: scheme and host in lower case, no
// trailing slash. Nothing else is folded — a different port is a different
// backend.
func (id MapIdentity) normal() MapIdentity {
	id.AssetType = strings.TrimSpace(id.AssetType)
	if id.AssetType == "" {
		id.AssetType = "map"
	}
	id.AssetID = strings.TrimSpace(id.AssetID)
	backend := strings.TrimRight(strings.TrimSpace(id.Backend), "/")
	if parsed, err := url.Parse(backend); err == nil && parsed.Host != "" {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		backend = strings.TrimRight(parsed.String(), "/")
	}
	id.Backend = backend
	return id
}

// Same reports whether two identities name one map.
func (id MapIdentity) Same(other MapIdentity) bool {
	a, b := id.normal(), other.normal()
	return a.AssetID != "" && a == b
}

// ErrLeakTest reports a leak test offered as a level.
var ErrLeakTest = errors.New("a leak test is a diagnostic, not a playable build: its BSP never went through a normal VIS and LIGHT")

// Diagnostic reports a build that answered a leak test rather than built a
// level: one an editor's request bound to its leak adapter (`leak_test`), or
// any build of a leak-test pipeline — the built-in ones by their adapter, a
// pinned one when isLeak says so — however it was started. A leak pipeline's
// BSP is QBSP's alone; it never went through VIS and LIGHT as a level.
func Diagnostic(m *Manifest, isLeak func(pipelineID string) bool) bool {
	if m == nil {
		return false
	}
	if m.LeakTest != nil {
		return true
	}
	if _, builtin := leakadapter.ForPipeline(m.Pipeline.ID); builtin {
		return true
	}
	return isLeak != nil && isLeak(m.Pipeline.ID)
}

// VerifyPlayable is [PlayableLevel] for a build somebody is about to play: a
// normal build, whose level is still the bytes the build recorded.
func VerifyPlayable(m *Manifest) (Playable, error) {
	if Diagnostic(m, nil) {
		return Playable{}, fmt.Errorf("%w: %w", ErrLeakTest, ErrNotPlayable)
	}
	level, err := PlayableLevel(m)
	if err != nil {
		return Playable{}, err
	}
	for _, output := range m.Outputs {
		if output.Missing || output.Path == "" || output.SHA256 == "" {
			continue
		}
		if output.Path != level.BSP && output.Path != level.Lit {
			continue
		}
		digest, _, err := digestFile(output.Path)
		if err != nil {
			return Playable{}, fmt.Errorf("its %s could not be read: %w", output.Name, ErrNotPlayable)
		}
		if digest != output.SHA256 {
			return Playable{}, fmt.Errorf("its %s was changed after the build (%s, recorded %s): %w",
				output.Name, digest, output.SHA256, ErrNotPlayable)
		}
	}
	return level, nil
}

// Latest chooses the build to play for one map. family is the engine's game
// family; empty accepts any known family. isLeak names the pipelines pinned as
// a game's leak test on this machine (nil: the built-in ones only).
func Latest(manifests []*Manifest, want MapIdentity, family string, isLeak func(string) bool) Selection {
	want = want.normal()
	out := Selection{State: LatestNone, Map: want}
	var candidates []*Manifest
	var leaks []*Manifest
	other := ""
	for _, m := range manifests {
		identity, err := IdentityOf(m)
		if err != nil || !identity.Same(want) {
			continue
		}
		switch {
		case Diagnostic(m, isLeak):
			leaks = append(leaks, m)
		case !m.Succeeded():
		case m.EngineFamily == "":
			// A build from before the manifest recorded its family: its game
			// is not known, and is not guessed.
		case family != "" && m.EngineFamily != family:
			other = m.EngineFamily
		default:
			candidates = append(candidates, m)
		}
	}
	out.LeakTests = len(leaks)
	sort.SliceStable(candidates, func(i, j int) bool { return newer(candidates[i], candidates[j]) })
	if len(candidates) == 0 {
		switch {
		case other != "":
			out.Reason = fmt.Sprintf("No playable build for this game yet: this map's builds are for %s.", other)
		case len(leaks) > 0:
			out.Reason = "No playable build yet: this map has only leak tests, which are diagnostics. Build it to play it."
		default:
			out.Reason = "No playable build yet."
		}
		return out
	}
	chosen := candidates[0]
	out.BuildID, out.Label, out.Pipeline = chosen.BuildID, chosen.Label, chosen.Pipeline.Name
	out.Family, out.FinishedAt = chosen.EngineFamily, finishedAt(chosen)
	if source, err := SourceOf(chosen); err == nil {
		out.Revision, out.RevisionID = source.Revision, source.RevisionID
	}
	for _, leak := range leaks {
		if newer(leak, chosen) {
			if source, err := SourceOf(leak); err == nil && source.Revision > out.NewerLeakTest {
				out.NewerLeakTest = source.Revision
			}
		}
	}
	level, err := VerifyPlayable(chosen)
	if err != nil {
		out.State = LatestUnavailable
		out.Reason = "The latest build's level is not usable: " + err.Error() + ". Build it again to play it."
		return out
	}
	out.State = LatestPlayable
	out.MapName = level.MapName
	return out
}

func finishedAt(m *Manifest) time.Time {
	if !m.FinishedAt.IsZero() {
		return m.FinishedAt
	}
	return m.StartedAt
}

// newer orders builds newest first: by when they finished, then by build id,
// which starts with the time it was minted and ends in random hex, so two
// builds that finished in the same instant are still ordered the same way
// every time.
func newer(a, b *Manifest) bool {
	ta, tb := finishedAt(a), finishedAt(b)
	if !ta.Equal(tb) {
		return ta.After(tb)
	}
	return a.BuildID > b.BuildID
}
