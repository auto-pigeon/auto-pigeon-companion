package release

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// What this build BUILDS, and what has been run on real hardware — which are
// two different questions and used to be answered by one sentence.
//
// # The correction
//
// `build/release.sh` produces six artifacts and every one of them is honest
// about being a build. None of them is evidence that the program starts,
// registers a URI scheme, finds a compiler, writes a PAK or composes an engine
// command on the machine it was built for. Cross-compilation proves
// buildability; it proves nothing else, and a release note that called all six
// "supported" was making a claim nobody had checked on four of them.
//
// So there are two axes here and neither is allowed to stand in for the other:
//
//	built                an artifact is produced for this target
//	native verification  something ran ON that target and said so
//
// # Five states, and two of them are never a pass
//
// The vocabulary is shared with `companion acceptance` and with
// `auto-pigeon-tools`' merge command, so a row means the same thing in the
// release notes, in the product's own output and in the platform matrix.
//
//	native_pass     a bundle from that platform passed
//	native_fail     a bundle from that platform failed
//	manual_pending  a native host exists and nobody has run it yet
//	build_only      an artifact is produced and no native host is declared
//	unsupported     no artifact is produced
//
// `build_only` and `manual_pending` are NEVER promoted to a pass by anything
// here. There is no code path from either to `native_pass` that does not go
// through a verification record, and a verification record names the bundle it
// came from.
//
// # Why the record is a committed file
//
// A verification is a decision somebody made after reading a bundle: these
// bytes, from that machine, on that date, are accepted as evidence. That is a
// reviewable act and belongs in version control beside the code it is about —
// not computed at runtime from whatever happens to be on a disk. The bundles
// themselves live under the mapper root and are merged by
// `auto-pigeon-tools/scripts/aucom-acceptance.sh matrix`, which cross-checks
// this file against them and reports a row this file claims and no bundle
// supports.

//go:embed native-support.json
var nativeSupportDocument []byte

// SupportState is one platform row's answer. See the file comment for why
// there are five and why two of them can never become a pass.
type SupportState string

const (
	NativePass    SupportState = "native_pass"
	NativeFail    SupportState = "native_fail"
	ManualPending SupportState = "manual_pending"
	BuildOnly     SupportState = "build_only"
	Unsupported   SupportState = "unsupported"
)

// NativeHost says who, if anyone, can run the kit on a target.
type NativeHost string

const (
	// HostAgent: an agent or the project owner can run it here today.
	HostAgent NativeHost = "agent"
	// HostOwnerManual: the project owner has such a machine and runs it by hand.
	HostOwnerManual NativeHost = "owner_manual"
	// HostNone: nobody has declared a machine. This is not a promise that one
	// will appear, and it is not a defect.
	HostNone NativeHost = "none"
)

// Target is one artifact target and what is known about it.
type Target struct {
	Target     string     `json:"target"`
	Built      bool       `json:"built"`
	NativeHost NativeHost `json:"native_host"`
	Note       string     `json:"note"`
}

// Verification is one accepted native result bundle.
//
// Every member is provenance: which bundle, which bytes, which build, when,
// and what it came to. A row here without a Digest is a claim with nothing
// behind it, and [LoadSupport] refuses one.
type Verification struct {
	Target           string `json:"target"`
	BundleID         string `json:"bundle_id"`
	Digest           string `json:"digest"`
	CompanionVersion string `json:"companion_version"`
	Verdict          string `json:"verdict"`
	ProducedAt       string `json:"produced_at"`
	Note             string `json:"note,omitempty"`
}

// Support is the whole declaration.
type Support struct {
	Schema        string         `json:"schema"`
	Targets       []Target       `json:"targets"`
	Verifications []Verification `json:"verifications"`
}

// SupportSchema is the declaration's format identifier.
const SupportSchema = "aucom.native-support/1.0"

// Row is one platform, its state, and what supports it.
type Row struct {
	Target        string         `json:"target"`
	Built         bool           `json:"built"`
	NativeHost    NativeHost     `json:"native_host"`
	State         SupportState   `json:"state"`
	Note          string         `json:"note"`
	Verifications []Verification `json:"verifications,omitempty"`
}

// LoadSupport decodes the committed declaration.
//
// Unknown fields are refused rather than ignored: a misspelled `native_host`
// that silently means "none" is exactly the failure a schema exists to catch,
// and this document is small enough that nobody needs the tolerance.
func LoadSupport() (Support, error) {
	decoder := json.NewDecoder(bytes.NewReader(nativeSupportDocument))
	decoder.DisallowUnknownFields()
	var support Support
	if err := decoder.Decode(&support); err != nil {
		return Support{}, fmt.Errorf("release: native-support.json: %w", err)
	}
	if support.Schema != SupportSchema {
		return Support{}, fmt.Errorf("release: native-support.json declares schema %q, not %q",
			support.Schema, SupportSchema)
	}
	seen := map[string]bool{}
	for _, target := range support.Targets {
		if !strings.Contains(target.Target, "/") {
			return Support{}, fmt.Errorf("release: %q is not an <os>/<arch> target", target.Target)
		}
		if seen[target.Target] {
			return Support{}, fmt.Errorf("release: %q appears twice", target.Target)
		}
		seen[target.Target] = true
		switch target.NativeHost {
		case HostAgent, HostOwnerManual, HostNone:
		default:
			return Support{}, fmt.Errorf("release: %q declares an unknown native host %q",
				target.Target, target.NativeHost)
		}
		if strings.TrimSpace(target.Note) == "" {
			return Support{}, fmt.Errorf("release: %q has no note saying why", target.Target)
		}
	}
	for _, verification := range support.Verifications {
		if !seen[verification.Target] {
			return Support{}, fmt.Errorf(
				"release: a verification names %q, which is not a declared target", verification.Target)
		}
		if strings.TrimSpace(verification.Digest) == "" || strings.TrimSpace(verification.BundleID) == "" {
			return Support{}, fmt.Errorf(
				"release: the %q verification names no bundle and digest, so it supports nothing",
				verification.Target)
		}
		if verification.Verdict != "pass" && verification.Verdict != "fail" {
			return Support{}, fmt.Errorf("release: the %q verification's verdict is %q, not pass or fail",
				verification.Target, verification.Verdict)
		}
	}
	return support, nil
}

// StateOf is the one derivation, and every consumer calls it.
//
// A target with no artifact is unsupported; with an artifact and no declared
// host it is build_only; with a host and no accepted bundle it is
// manual_pending; and only a bundle moves it to native_pass or native_fail. A
// failing bundle wins over a passing one for the same build, because "it worked
// the second time" and "it works" are different facts (`AGENTS.md` §3d).
func StateOf(target Target, verifications []Verification) SupportState {
	if !target.Built {
		return Unsupported
	}
	sawPass := false
	for _, verification := range verifications {
		if verification.Target != target.Target {
			continue
		}
		if verification.Verdict == "fail" {
			return NativeFail
		}
		sawPass = true
	}
	if sawPass {
		return NativePass
	}
	if target.NativeHost == HostNone {
		return BuildOnly
	}
	return ManualPending
}

// Rows is the declaration resolved into what a reader sees.
func Rows() ([]Row, error) {
	support, err := LoadSupport()
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(support.Targets))
	for _, target := range support.Targets {
		row := Row{
			Target: target.Target, Built: target.Built, NativeHost: target.NativeHost,
			State: StateOf(target, support.Verifications), Note: target.Note,
		}
		for _, verification := range support.Verifications {
			if verification.Target == target.Target {
				row.Verifications = append(row.Verifications, verification)
			}
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Target < rows[j].Target })
	return rows, nil
}

// BuiltTargets is the target list a release produces, for the test that holds
// `build/release.sh` and this file to the same answer.
func BuiltTargets() ([]string, error) {
	support, err := LoadSupport()
	if err != nil {
		return nil, err
	}
	var targets []string
	for _, target := range support.Targets {
		if target.Built {
			targets = append(targets, target.Target)
		}
	}
	sort.Strings(targets)
	return targets, nil
}
