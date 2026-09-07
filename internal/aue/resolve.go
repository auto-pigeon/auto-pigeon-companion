package aue

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/acquire"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// ErrNoCompatibilityManifest reports a catalogue that publishes no
// compatibility manifest.
//
// Its own error because there is nothing for the user to fix on this machine:
// the publisher has to publish one. A Companion must not choose an extractor
// version for itself when nobody has said which one goes with it — a version
// NUMBER does not imply a contract, which is exactly the assumption a rebuilt
// or forked extractor breaks.
var ErrNoCompatibilityManifest = errors.New(
	"aue: this catalogue publishes no compatibility manifest, so nothing says which extractor build " +
		"goes with this Companion")

// Resolver turns "run the extractor" into a verified executable.
type Resolver struct {
	// Acquirer is the shared verifier and cache. The SAME one every other
	// managed tool uses — this is not a second updater, and a change to the
	// verification chain applies here without anything being kept in step.
	Acquirer *acquire.Acquirer

	// CompanionVersion is this build's version, which is what the compatibility
	// manifest is keyed on.
	CompanionVersion string

	// Component is the package id. Empty means [ComponentID]; a field so a test
	// can drive the whole path against a fixture package.
	Component string

	// Override is the developer override path. Empty means none; when it is
	// set, Resolve returns an override runner and touches no catalogue.
	Override string

	// Platform is the machine. Zero means the running one; a field so a test
	// can ask what happens on a platform this release does not publish.
	Platform profile.Platform

	// Install controls whether a missing build may be downloaded. False means
	// "use what is already installed", which is what an offline run and a
	// status query want.
	Install bool

	// PinPath is where the last verified requirement is recorded. Empty means
	// nothing is recorded and nothing is read, which makes an offline
	// resolution impossible rather than approximate.
	PinPath string

	// Offline says the Companion was told not to use the network. It is passed
	// here as well as to the acquirer because it changes WHICH answer is
	// authoritative: online, the signed manifest; offline, the requirement this
	// machine last verified. It is never inferred from a failure — a fetch that
	// went wrong is a refusal, not permission to use an older answer.
	Offline bool
}

func (r *Resolver) component() string {
	if strings.TrimSpace(r.Component) != "" {
		return r.Component
	}

	return ComponentID
}

func (r *Resolver) platform() profile.Platform {
	if !r.Platform.Zero() {
		return r.Platform
	}

	return profile.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// Plan is what a resolution would do, without doing it.
type Plan struct {
	// Override is set when a developer override is configured, and then
	// nothing else here is.
	Override string
	// Requirement is the compatibility rule that chose the version.
	Requirement catalog.Requirement
	// Installed is the cache entry, when one is already present.
	Installed *acquire.Install
	// Note is one line for a person.
	Note string
}

// Plan says which build this Companion would run, and whether it already has
// it, without downloading anything.
func (r *Resolver) Plan(ctx context.Context) (*Plan, error) {
	if strings.TrimSpace(r.Override) != "" {
		return &Plan{Override: r.Override, Note: UnverifiedNote}, nil
	}
	requirement, err := r.requirement(ctx)
	if err != nil {
		return nil, err
	}
	plan := &Plan{Requirement: requirement, Note: requirementNote(requirement)}
	if installed, findErr := r.Acquirer.FindInstalled(r.component(), requirement.Version); findErr == nil {
		plan.Installed = installed
	}

	return plan, nil
}

// requirement resolves the compatibility rule for this build on this machine.
//
// # Offline is a different authority, not a weaker check
//
// Online, the answer comes from a signed manifest and is RECORDED. Offline, it
// comes from the recording — the requirement this machine last verified for
// exactly this component, this Companion version and this platform. Nothing is
// skipped either way: the protocol handshake still runs, against the minimum
// the pin carries.
//
// The fallback happens ONLY because the caller said `Offline`. A verification
// that failed, a rollback attempt, an expired document or an unreachable
// server is a refusal and never becomes "use the older answer" — which is the
// difference between an offline mode and a way around the catalogue.
func (r *Resolver) requirement(ctx context.Context) (catalog.Requirement, error) {
	if r.Offline {
		pin, err := LoadPin(r.PinPath)
		if err != nil {
			return catalog.Requirement{}, err
		}
		if !pin.Matches(r.component(), r.CompanionVersion, r.platform()) {
			return catalog.Requirement{}, fmt.Errorf("%w: offline, and this machine has no recorded "+
				"requirement for %s on %s; run once with the network to resolve one",
				catalog.ErrOffline, r.component(), r.platform())
		}

		return pin.Requirement(), nil
	}

	verified, err := r.Acquirer.Catalog(ctx)
	if err != nil {
		return catalog.Requirement{}, err
	}
	if verified.Compatibility == nil {
		return catalog.Requirement{}, ErrNoCompatibilityManifest
	}
	requirement, err := verified.Compatibility.Requirement(r.component(), r.CompanionVersion, r.platform())
	if err != nil {
		return catalog.Requirement{}, err
	}
	pin := &Pin{
		SchemaVersion: PinSchemaVersion,
		Component:     r.component(), CompanionVersion: r.CompanionVersion, Platform: r.platform(),
		Version: requirement.Version, MinProtocol: requirement.MinProtocol,
		CompatibilityID:     verified.Compatibility.DocumentID,
		CompatibilitySerial: verified.Compatibility.Serial,
		ResolvedAt:          verified.VerifiedAt,
	}
	if err := SavePin(r.PinPath, pin); err != nil {
		return catalog.Requirement{}, err
	}

	return requirement, nil
}

// Status is what this Companion can say about its extractor without touching
// the network.
//
// It is what a status page and a status command read. A resolution is a
// network operation and a status is not: a page that had to fetch a catalogue
// to say whether an extractor was installed would be a page that says nothing
// on a train.
type Status struct {
	Mode      string `json:"mode"`
	Available bool   `json:"available"`
	Verified  bool   `json:"verified"`
	// Version is the installed version, when one is installed.
	Version string `json:"version,omitempty"`
	// Required is the version the last verified requirement named. It differs
	// from Version exactly when an upgrade is pending, which is a fact worth
	// showing rather than hiding behind a single "up to date".
	Required    string `json:"required,omitempty"`
	MinProtocol string `json:"min_protocol,omitempty"`
	// Reason says why nothing is available, and is empty when something is.
	Reason string `json:"reason,omitempty"`
	// Note is the unverified warning on an override.
	Note string `json:"note,omitempty"`
}

// Status answers without the network.
func (r *Resolver) Status() Status {
	if path := strings.TrimSpace(r.Override); path != "" {
		status := Status{Mode: ModeDeveloperOverride, Available: true, Note: UnverifiedNote}
		if err := checkExecutable(path); err != nil {
			status.Available, status.Reason = false, err.Error()
		}

		return status
	}

	status := Status{Mode: ModeManaged}
	pin, err := LoadPin(r.PinPath)
	if err != nil {
		status.Reason = err.Error()

		return status
	}
	if pin.Matches(r.component(), r.CompanionVersion, r.platform()) {
		status.Required, status.MinProtocol = pin.Version, pin.MinProtocol
	}

	installed, err := r.Acquirer.FindInstalled(r.component(), status.Required)
	if err != nil || installed == nil {
		status.Reason = fmt.Sprintf("no verified %s is installed", ProgramName)
		if status.Required != "" {
			status.Reason = fmt.Sprintf("%s %s is required and is not installed", ProgramName, status.Required)
		}

		return status
	}
	status.Available, status.Verified, status.Version = true, true, installed.Version

	return status
}

// Resolve produces a runner for an executable this Companion may run.
//
// # The two ways, and the absence of a third
//
// A developer override short-circuits everything: no catalogue is fetched, no
// version is resolved, and the runner it returns says it is unverified. That is
// the ONLY unverified path, it is taken only because the user set an
// environment variable, and it never happens as a consequence of something
// else failing.
//
// Otherwise: the compatibility manifest names a version, the acquirer installs
// or finds it, `Use` re-checks the cached executables against the install
// record and against the sticky revocation list, the platform is checked
// again, and the executable is asked what protocol it speaks before it is
// handed to anybody.
//
// Every failure along that path is a refusal. There is no step at which "the
// catalogue could not be verified" or "the manifest names no build for this
// platform" becomes an execution that happens anyway.
func (r *Resolver) Resolve(ctx context.Context) (*ProcessRunner, error) {
	if path := strings.TrimSpace(r.Override); path != "" {
		if err := checkExecutable(path); err != nil {
			return nil, err
		}

		return NewOverrideRunner(path), nil
	}

	requirement, err := r.requirement(ctx)
	if err != nil {
		return nil, err
	}

	install, err := r.Acquirer.FindInstalled(r.component(), requirement.Version)
	if err != nil || install == nil {
		if !r.Install {
			return nil, fmt.Errorf("%w: %s %s is required and is not installed",
				ErrNoExtractor, ProgramName, requirement.Version)
		}
		install, err = r.Acquirer.Install(ctx, r.component(), requirement.Version)
		if err != nil {
			return nil, err
		}
	}

	// `Use` is what re-verifies. It re-hashes the entry's executables against
	// the install record and consults the sticky revocation list, so an entry
	// that was fine when it was installed and has been edited since — or has
	// been withdrawn since — is refused here rather than executed.
	install, root, err := r.Acquirer.Use(install.Digest)
	if err != nil {
		return nil, err
	}

	// Checked again, and against the INSTALL record rather than against what
	// was asked for. A cache entry that names another platform is a verified
	// artifact this machine cannot execute, and the failure it produces
	// otherwise is an exec error nobody can act on.
	if install.Platform != r.platform() {
		return nil, fmt.Errorf("aue: the installed %s is for %s and this machine is %s",
			ProgramName, install.Platform, r.platform())
	}

	// One executable, no archive root. That is exactly what the extractor's own
	// release manifest produces — a bare `file` artifact per platform — and the
	// check is here rather than assumed because a package that declared an
	// archive with a nested root would resolve to a path this code composed
	// wrongly rather than to an error. Publishing one would be a change to the
	// extractor's build script and to this function together.
	executables := install.Executables
	if len(executables) != 1 {
		return nil, fmt.Errorf("aue: %s %s declares %d executables and this Companion runs one",
			ProgramName, install.Version, len(executables))
	}
	if install.Root != "" {
		return nil, fmt.Errorf("aue: %s %s is packaged under the archive root %q, which this Companion "+
			"does not resolve; the extractor publishes a bare executable per platform",
			ProgramName, install.Version, install.Root)
	}
	path := filepath.Join(root, filepath.FromSlash(executables[0]))
	if err := checkExecutable(path); err != nil {
		return nil, err
	}

	runner := &ProcessRunner{path: path, provenance: fromInstall(install, path, requirement.MinProtocol)}

	// The handshake, before this runner is handed to anybody. A verified
	// executable is not automatically one this build can talk to.
	if err := r.handshake(ctx, runner, requirement.MinProtocol); err != nil {
		return nil, err
	}

	return runner, nil
}

// protocolDocument is the part of the extractor's `protocol --json` this
// Companion reads.
//
// A transcription, because the extractor is a separate module and neither
// imports the other — the same arrangement `auto-pigeon-backend` uses for the
// two extractor contracts it reads. Unknown members are IGNORED rather than
// refused: a minor protocol bump may add a field, and refusing one would make
// every additive change breaking, which is exactly what the major/minor split
// exists to avoid.
type protocolDocument struct {
	SchemaVersion string `json:"schema_version"`
	Protocol      string `json:"protocol"`
	Version       string `json:"version"`
	License       struct {
		SPDX                string `json:"spdx"`
		CorrespondingSource string `json:"corresponding_source"`
	} `json:"license"`
}

// handshake asks the executable what contract it speaks and refuses it if this
// build cannot drive it.
func (r *Resolver) handshake(ctx context.Context, runner *ProcessRunner, minProtocol string) error {
	var document protocolDocument
	if err := runner.RunJSON(ctx, &document, "protocol", "--json"); err != nil {
		return fmt.Errorf("aue: asking %s which invocation protocol it speaks: %w", ProgramName, err)
	}
	if strings.TrimSpace(document.Protocol) == "" {
		return fmt.Errorf("aue: %s declares no invocation protocol, so nothing can tell whether this "+
			"Companion may drive it", ProgramName)
	}
	if err := catalog.ProtocolSatisfies(document.Protocol, minProtocol); err != nil {
		return fmt.Errorf("aue: %s %s: %w", ProgramName, document.Version, err)
	}
	runner.provenance.Protocol = document.Protocol

	return nil
}

// Handshake performs the protocol check against an already-built runner.
//
// Exported for the override path, where there is no compatibility rule to
// supply a minimum and a caller has to name one. It is deliberately NOT called
// automatically for an override: an override exists so somebody can drive a
// build that does not exist yet, and refusing to run it for speaking tomorrow's
// protocol would defeat the only reason it exists. A caller that wants the
// check asks for it.
func Handshake(ctx context.Context, runner *ProcessRunner, minProtocol string) error {
	resolver := &Resolver{}

	return resolver.handshake(ctx, runner, minProtocol)
}

// checkExecutable is the last thing before an exec: the path is a real, regular
// file and the operating system will run it.
//
// A local copy of the check `internal/acquire` makes on a cache entry, not a
// substitute for it. That one re-hashes the bytes against the install record;
// this one catches the case where the path composed above does not name a file
// at all, and produces a sentence instead of an exec error.
func checkExecutable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("aue: %s is not there", path)
		}

		return fmt.Errorf("aue: checking %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("aue: %s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("aue: %s is not executable", path)
	}

	return nil
}
