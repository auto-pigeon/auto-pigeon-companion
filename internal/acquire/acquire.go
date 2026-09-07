package acquire

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Options is everything an [Acquirer] needs to know about this machine.
//
// Every path and address is passed in rather than resolved here. The Companion
// has one place that turns configuration into locations — internal/config — and
// a package that reached for the user's home directory on its own would be a
// second one, which is how a test ends up writing into the developer's real
// cache.
type Options struct {
	// CacheDir holds downloaded packages. Under the OS cache directory: its
	// contents are re-downloadable.
	CacheDir string
	// StatePath is the catalogue trust state — serials and revocations. Under
	// the *config* directory, because it records decisions, and clearing a
	// cache must not reset a rollback ratchet. See [catalog.State].
	StatePath string
	// AcceptancePath is the licence-acknowledgement record, also configuration
	// rather than cache.
	AcceptancePath string
	// AnchorsPath is the trust anchor file. Empty means no anchors, which
	// means every managed download is refused.
	AnchorsPath string
	// CatalogURL is the configured catalogue address.
	CatalogURL string
	// Offline forbids every network access. See the package comment for what
	// it does and does not change.
	Offline bool
	// HTTP is the transport for both the catalogue and the artifacts. A field
	// so a test can hand it the client of an in-process TLS fixture.
	HTTP *http.Client
	// Now is the clock.
	Now func() time.Time
}

// Acquirer resolves a profile's acquisition options into executables on this
// machine.
type Acquirer struct {
	options    Options
	cache      *Cache
	downloader *Downloader
	// verified memoises the catalogue for the life of this value. One
	// verification per command rather than one per package: the chain is the
	// same and re-fetching it between two installs would be a second chance
	// for the answer to change halfway through a user's decision.
	verified *catalog.Verified
}

// New prepares an acquirer.
func New(options Options) (*Acquirer, error) {
	cache, err := OpenCache(options.CacheDir)
	if err != nil {
		return nil, err
	}
	return &Acquirer{
		options:    options,
		cache:      cache,
		downloader: &Downloader{HTTP: options.HTTP},
	}, nil
}

// Cache is the store this acquirer installs into.
func (a *Acquirer) Cache() *Cache { return a.cache }

// Offline reports whether the network is forbidden.
func (a *Acquirer) Offline() bool { return a.options.Offline }

func (a *Acquirer) now() time.Time {
	if a.options.Now != nil {
		return a.options.Now().UTC()
	}
	return time.Now().UTC()
}

// State reads the catalogue trust state.
func (a *Acquirer) State() (*catalog.State, error) {
	if strings.TrimSpace(a.options.StatePath) == "" {
		return catalog.NewState(), nil
	}
	return catalog.LoadState(a.options.StatePath)
}

// Acceptances reads the local licence acknowledgements.
func (a *Acquirer) Acceptances() (*Acceptances, error) {
	if strings.TrimSpace(a.options.AcceptancePath) == "" {
		return &Acceptances{SchemaVersion: AcceptanceSchemaVersion}, nil
	}
	return LoadAcceptances(a.options.AcceptancePath)
}

// Catalog fetches and verifies the catalogue chain.
//
// Every failure here is a refusal, and that is the single most important
// property in this file. There is no path from "the network is down", "the
// signature did not verify", "the catalogue expired" or "no anchor is
// configured" to a download that happens anyway. A program that falls back to
// unverified execution when verification fails has not implemented
// verification; it has implemented a preference.
func (a *Acquirer) Catalog(ctx context.Context) (*catalog.Verified, error) {
	if a.verified != nil {
		return a.verified, nil
	}
	if a.options.Offline {
		return nil, fmt.Errorf("%w: the catalogue cannot be fetched while offline; "+
			"already-installed packages are still usable", catalog.ErrOffline)
	}
	if strings.TrimSpace(a.options.AnchorsPath) == "" {
		return nil, catalog.ErrNoAnchors
	}
	anchors, err := catalog.LoadAnchors(a.options.AnchorsPath)
	if err != nil {
		return nil, err
	}
	state, err := a.State()
	if err != nil {
		return nil, err
	}
	client := &catalog.Client{HTTP: a.options.HTTP, BaseURL: a.options.CatalogURL}
	keyringEnvelope, catalogEnvelope, err := client.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	verifier := &catalog.Verifier{Anchors: anchors, State: state, Now: a.options.Now}
	verified, err := verifier.Verify(keyringEnvelope, catalogEnvelope)
	if err != nil {
		// The state is still written on a failed verification when the failure
		// came *after* a document was accepted — a keyring that verified and a
		// catalogue that did not still advanced the keyring's ratchet, and
		// forgetting that would let the same replay be tried again.
		_ = a.saveState(state)
		return nil, err
	}
	if err := a.saveState(state); err != nil {
		return nil, err
	}
	a.verified = verified
	return verified, nil
}

func (a *Acquirer) saveState(state *catalog.State) error {
	if strings.TrimSpace(a.options.StatePath) == "" || state == nil {
		return nil
	}
	return catalog.SaveState(a.options.StatePath, state)
}

// Plan is what installing a package would involve, assembled before anything is
// downloaded so that a user is asked with the facts in front of them.
type Plan struct {
	Package  catalog.Package
	Artifact catalog.Artifact
	Platform profile.Platform
	// URL is redacted: scheme, host and path. See [catalog.RedactURL].
	URL              string
	CatalogID        string
	CatalogSerial    int64
	Signer           string
	AlreadyInstalled bool
	// NeedsAcceptance is true when the licence requires a notice and this
	// machine has no record of it having been shown.
	NeedsAcceptance bool
}

// Text is the summary shown before the first download: what it is, who
// publishes it, under what licence, from where, how big, and who vouched.
func (p *Plan) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s (%s)\n", p.Package.Name, p.Package.Version, p.Package.ID)
	if p.Package.Summary != "" {
		fmt.Fprintf(&b, "  %s\n", p.Package.Summary)
	}
	fmt.Fprintf(&b, "  licence:  %s", p.Package.License.SPDX)
	if p.Package.License.Name != "" {
		fmt.Fprintf(&b, " — %s", p.Package.License.Name)
	}
	b.WriteString("\n")
	if p.Package.License.URL != "" {
		fmt.Fprintf(&b, "  terms:    %s\n", p.Package.License.URL)
	}
	if p.Package.License.CorrespondingSource != "" {
		fmt.Fprintf(&b, "  source for this binary: %s\n", p.Package.License.CorrespondingSource)
	}
	if p.Package.Source.Homepage != "" {
		fmt.Fprintf(&b, "  project:  %s\n", p.Package.Source.Homepage)
	}
	if p.Package.Source.Repository != "" {
		fmt.Fprintf(&b, "  code:     %s\n", p.Package.Source.Repository)
	}
	fmt.Fprintf(&b, "  download: %s\n", p.URL)
	fmt.Fprintf(&b, "  platform: %s, %s, %s\n", p.Platform, formatBytes(p.Artifact.Size), p.Artifact.Kind)
	fmt.Fprintf(&b, "  digest:   %s\n", p.Artifact.SHA256)
	fmt.Fprintf(&b, "  vouched:  key %s, catalogue %s serial %d\n", p.Signer, p.CatalogID, p.CatalogSerial)
	fmt.Fprintf(&b, "\n  %s\n", catalog.Aggregation)
	if p.AlreadyInstalled {
		b.WriteString("\n  Already downloaded and verified on this machine; installing again is a no-op.\n")
	}
	if p.NeedsAcceptance {
		b.WriteString("\n  This licence requires that you be shown a notice before the program is obtained.\n")
		if notice := strings.TrimSpace(p.Package.License.Notice); notice != "" {
			b.WriteString("\n")
			for _, line := range strings.Split(notice, "\n") {
				fmt.Fprintf(&b, "    %s\n", line)
			}
		}
		b.WriteString("\n  Record that you have read it with `companion acquire accept " + p.Package.ID +
			" --version " + p.Package.Version + "`.\n" +
			"  That record is a local note that the notice was shown. It is not a licence, it grants\n" +
			"  you nothing, and it does not change what the licence requires of anyone.\n")
	}
	return b.String()
}

// ErrLicenseNotAccepted reports a package whose licence notice has not been
// acknowledged on this machine.
var ErrLicenseNotAccepted = errors.New("acquire: the licence notice has not been acknowledged")

// Plan resolves a package to what installing it would do.
func (a *Acquirer) Plan(ctx context.Context, id, version string) (*Plan, error) {
	verified, err := a.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	state, err := a.State()
	if err != nil {
		return nil, err
	}
	pkg, artifact, err := verified.Artifact(state, id, version, CurrentPlatform())
	if err != nil {
		return nil, err
	}
	accepted, err := a.Acceptances()
	if err != nil {
		return nil, err
	}
	return &Plan{
		Package:          pkg,
		Artifact:         artifact,
		Platform:         artifact.Platform,
		URL:              catalog.RedactURL(artifact.URL),
		CatalogID:        verified.Catalog.CatalogID,
		CatalogSerial:    verified.Catalog.Serial,
		Signer:           artifact.Signer,
		AlreadyInstalled: a.cache.Has(artifact.SHA256),
		NeedsAcceptance:  pkg.RequiresAcceptance && !accepted.Covers(pkg),
	}, nil
}

// Accept records that a package's licence notice was shown and read.
func (a *Acquirer) Accept(ctx context.Context, id, version string) (*Plan, error) {
	plan, err := a.Plan(ctx, id, version)
	if err != nil {
		return nil, err
	}
	accepted, err := a.Acceptances()
	if err != nil {
		return nil, err
	}
	accepted.Record(plan.Package, a.now())
	if path := strings.TrimSpace(a.options.AcceptancePath); path != "" {
		if err := SaveAcceptances(path, accepted); err != nil {
			return nil, err
		}
	}
	plan.NeedsAcceptance = false
	return plan, nil
}

// Install performs a managed download, or returns the verified entry that is
// already there.
func (a *Acquirer) Install(ctx context.Context, id, version string) (*Install, error) {
	if a.options.Offline {
		install, err := a.FindInstalled(id, version)
		if err != nil {
			return nil, fmt.Errorf("%w: %s is not already installed, and installing needs the network", catalog.ErrOffline, id)
		}
		if _, _, err := a.Use(install.Digest); err != nil {
			return nil, err
		}
		return install, nil
	}
	plan, err := a.Plan(ctx, id, version)
	if err != nil {
		return nil, err
	}
	if plan.NeedsAcceptance {
		return nil, fmt.Errorf("%w: %s %s is under %s\n\n%s", ErrLicenseNotAccepted,
			plan.Package.ID, plan.Package.Version, plan.Package.License.SPDX, plan.Text())
	}
	if a.cache.Has(plan.Artifact.SHA256) {
		install, _, err := a.Use(plan.Artifact.SHA256)
		if err != nil {
			return nil, err
		}
		return install, nil
	}
	return a.install(ctx, plan)
}

func (a *Acquirer) install(ctx context.Context, plan *Plan) (*Install, error) {
	staged, err := a.cache.stage()
	if err != nil {
		return nil, err
	}
	// Removed on every path that does not rename it away. An interrupted
	// download leaves nothing: the staging area is not on anybody's path, and
	// the next attempt starts from a fresh directory rather than resuming into
	// bytes whose provenance is now two downloads.
	defer os.RemoveAll(staged)

	download := filepath.Join(staged, "download")
	if err := a.downloader.download(ctx, plan.Artifact, download); err != nil {
		return nil, err
	}

	payload := filepath.Join(staged, payloadDir)
	var files []FileRecord
	switch plan.Artifact.Kind {
	case catalog.KindFile:
		record, err := installSingleFile(download, payload, plan.Artifact.File)
		if err != nil {
			return nil, err
		}
		files = []FileRecord{record}
	default:
		files, err = unpack(plan.Artifact.Kind, download, payload,
			plan.Artifact.Size, plan.Artifact.UnpackedSize, plan.Artifact.ExecutablePaths())
		if err != nil {
			return nil, err
		}
	}
	if err := os.Remove(download); err != nil {
		return nil, fmt.Errorf("acquire: removing the download after unpacking: %w", err)
	}

	executables := plan.Artifact.ExecutablePaths()
	present := make(map[string]bool, len(files))
	for _, file := range files {
		present[file.Path] = true
	}
	for _, name := range executables {
		if !present[name] {
			return nil, fmt.Errorf("acquire: %s %s declares the executable %q and the download does not contain it",
				plan.Package.ID, plan.Package.Version, name)
		}
	}

	install := &Install{
		Digest:             plan.Artifact.SHA256,
		PackageID:          plan.Package.ID,
		Version:            plan.Package.Version,
		Name:               plan.Package.Name,
		Program:            plan.Package.Program,
		Platform:           plan.Artifact.Platform,
		Kind:               plan.Artifact.Kind,
		Size:               plan.Artifact.Size,
		UnpackedSize:       plan.Artifact.UnpackedSize,
		License:            plan.Package.License,
		RequiresAcceptance: plan.Package.RequiresAcceptance,
		Source:             plan.Package.Source,
		Aggregation:        catalog.Aggregation,
		Signer:             plan.Artifact.Signer,
		CatalogID:          plan.CatalogID,
		CatalogSerial:      plan.CatalogSerial,
		InstalledAt:        a.now(),
		Root:               plan.Artifact.RootPath(),
		Executables:        executables,
		Files:              files,
		SourceURL:          plan.URL,
	}
	if a.verified != nil {
		install.CatalogDigest = a.verified.CatalogDigest
		install.KeyringDigest = a.verified.KeyringDigest
	}
	if _, err := a.cache.commit(staged, install); err != nil {
		return nil, err
	}
	return install, nil
}

// FindInstalled returns the cache entry for a package, newest first when no
// version is pinned. It needs no catalogue, which is what makes offline use
// possible.
func (a *Acquirer) FindInstalled(id, version string) (*Install, error) {
	installs, err := a.cache.List()
	if err != nil {
		return nil, err
	}
	platform := CurrentPlatform()
	var best *Install
	for _, install := range installs {
		if install.PackageID != id || install.Platform != platform {
			continue
		}
		if version != "" && install.Version != version {
			continue
		}
		if best == nil || laterInstall(install, best) {
			best = install
		}
	}
	if best == nil {
		if version == "" {
			return nil, fmt.Errorf("%w: %s for %s", ErrNotInstalled, id, platform)
		}
		return nil, fmt.Errorf("%w: %s %s for %s", ErrNotInstalled, id, version, platform)
	}
	return best, nil
}

// laterInstall prefers the newer upstream version, and the more recent install
// when the versions are the same string.
func laterInstall(candidate, best *Install) bool {
	if candidate.Version != best.Version {
		return catalog.LaterVersion(candidate.Version, best.Version)
	}
	return candidate.InstalledAt.After(best.InstalledAt)
}

// Use returns an installed package's `tool_root`, having checked that it has
// not been revoked and has not been changed since it was installed.
//
// This is the path a job takes, and it is deliberately the same online and off.
// Nothing here fetches anything: the install record already says what was
// verified and by whom, and the sticky revocation list is local.
func (a *Acquirer) Use(digest string) (*Install, string, error) {
	state, err := a.State()
	if err != nil {
		return nil, "", err
	}
	if revocation, revoked := state.ArtifactRevocation(digest); revoked {
		return nil, "", fmt.Errorf("%w: this build was withdrawn on %s: %s",
			catalog.ErrRevoked, revocation.At.UTC().Format(time.RFC3339), revocation.Reason)
	}
	return a.cache.Use(digest)
}

// Request is one profile's acquisition question.
type Request struct {
	// Option is the route the user chose, from the profile.
	Option profile.AcquisitionOption
	// Executables is the profile's declared executables, whose `file` members
	// resolve under whatever root the chosen route produces.
	Executables []profile.Executable
	// Platform is the machine. Zero means the running one.
	Platform profile.Platform
	// UserPath is where the user pointed, for `user_path`.
	UserPath string
	// Roots are the configured roots, for `already_installed`.
	Roots map[string]string
	// Version pins a catalogue version, for `managed_download`. Empty means
	// the newest the catalogue offers.
	Version string
	// LookPath resolves a command on PATH; nil means exec.LookPath. A field so
	// a `system_path` resolution is testable without installing anything.
	LookPath func(string) (string, error)
}

// Result is where a profile's executables are on this machine.
type Result struct {
	Mode profile.AcquisitionMode
	// ToolRoot is the directory the profile's executable paths resolve under.
	// Empty for `system_path`, where each executable was found independently
	// and there is no common root.
	ToolRoot string
	// Executables maps each declared name to an absolute path.
	Executables map[string]string
	// Install is the cache entry, for `managed_download` only.
	Install *Install
	// Description is one sentence about where these came from, for the record
	// and for the user.
	Description string
}

// Resolve turns one acquisition option into executables on this machine.
//
// The four routes end in the same shape on purpose. A caller — the CLI, the
// server, whatever writes a binding — asks one question and gets one answer,
// and the difference between "the user already had this" and "we downloaded
// and verified it" is a field on the answer rather than four call sites.
func (a *Acquirer) Resolve(ctx context.Context, request Request) (*Result, error) {
	platform := request.Platform
	if platform.Zero() {
		platform = CurrentPlatform()
	}
	if len(request.Executables) == 0 {
		return nil, errors.New("acquire: the profile declares no executables")
	}
	switch request.Option.Mode {
	case profile.AcquireUserPath:
		return a.resolveRoot(request, platform, request.UserPath, profile.AcquireUserPath,
			"a location you chose; nothing has verified it")
	case profile.AcquireSystemPath:
		return a.resolveSystemPath(request, platform)
	case profile.AcquireAlreadyInstalled:
		return a.resolveAlreadyInstalled(request, platform)
	case profile.AcquireManagedDownload:
		return a.resolveManagedDownload(ctx, request, platform)
	}
	return nil, fmt.Errorf("acquire: %q is not an acquisition mode", request.Option.Mode)
}

// resolveRoot is the shared tail of every route that produces a directory: the
// profile's own executable paths are resolved under it, and each one is checked
// to be a regular file that is actually there.
func (a *Acquirer) resolveRoot(request Request, platform profile.Platform, root string, mode profile.AcquisitionMode, description string) (*Result, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("acquire: %s needs a directory and none was given", mode)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("acquire: resolving %s: %w", root, err)
	}
	executables := make(map[string]string, len(request.Executables))
	for _, declared := range request.Executables {
		relative := expandExeSuffix(declared.File, platform)
		if err := catalog.CheckArchivePath(relative); err != nil {
			return nil, fmt.Errorf("acquire: the profile's executable %q is at %q, which %w", declared.Name, relative, err)
		}
		path := filepath.Join(absolute, filepath.FromSlash(relative))
		if !withinRoot(absolute, path) {
			return nil, fmt.Errorf("acquire: the profile's executable %q resolves outside %s", declared.Name, absolute)
		}
		if err := checkExecutable(path); err != nil {
			return nil, err
		}
		executables[declared.Name] = path
	}
	return &Result{Mode: mode, ToolRoot: absolute, Executables: executables, Description: description}, nil
}

func (a *Acquirer) resolveSystemPath(request Request, platform profile.Platform) (*Result, error) {
	look := request.LookPath
	if look == nil {
		look = exec.LookPath
	}
	commands := request.Option.Commands
	if len(commands) == 0 {
		return nil, errors.New("acquire: the acquisition option lists no commands to look for on PATH")
	}
	// One command per declared executable, matched by name, with the option's
	// list as the set of names that are allowed to be looked up. A profile that
	// declares three executables and one command is a profile that cannot be
	// resolved this way, and saying so is better than resolving one of three.
	allowed := make(map[string]bool, len(commands))
	for _, command := range commands {
		allowed[command] = true
	}
	executables := make(map[string]string, len(request.Executables))
	for _, declared := range request.Executables {
		name := declared.Name
		if !allowed[name] {
			return nil, fmt.Errorf("acquire: this route looks for %s on PATH and the profile also declares %q; "+
				"a PATH lookup can only find the commands the option names", strings.Join(commands, ", "), name)
		}
		path, err := look(name + platform.ExeSuffix())
		if err != nil {
			return nil, fmt.Errorf("acquire: %s is not on PATH", name+platform.ExeSuffix())
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("acquire: resolving %s: %w", path, err)
		}
		if err := checkExecutable(absolute); err != nil {
			return nil, err
		}
		executables[name] = absolute
	}
	return &Result{
		Mode:        profile.AcquireSystemPath,
		Executables: executables,
		Description: "found on PATH; whoever installed it is who this machine already trusts",
	}, nil
}

func (a *Acquirer) resolveAlreadyInstalled(request Request, platform profile.Platform) (*Result, error) {
	role := request.Option.RelativeTo
	base, ok := request.Roots[role]
	if !ok || strings.TrimSpace(base) == "" {
		return nil, fmt.Errorf("acquire: this route is relative to the %s root and no %s is configured", role, role)
	}
	absoluteBase, err := filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("acquire: resolving %s: %w", base, err)
	}
	root := absoluteBase
	if sub := request.Option.Path; sub != "" {
		if err := catalog.CheckArchivePath(sub); err != nil {
			return nil, fmt.Errorf("acquire: the acquisition path %q %w", sub, err)
		}
		root = filepath.Join(absoluteBase, filepath.FromSlash(sub))
		if !withinRoot(absoluteBase, root) {
			return nil, fmt.Errorf("acquire: the acquisition path %q resolves outside the %s root", sub, role)
		}
	}
	return a.resolveRoot(request, platform, root, profile.AcquireAlreadyInstalled,
		"already on this machine, under the "+role+" you configured")
}

func (a *Acquirer) resolveManagedDownload(ctx context.Context, request Request, platform profile.Platform) (*Result, error) {
	if request.Option.CatalogPackage == "" {
		return nil, errors.New("acquire: the acquisition option names no catalogue package")
	}
	install, err := a.Install(ctx, request.Option.CatalogPackage, request.Version)
	if err != nil {
		return nil, err
	}
	entry, err := a.cache.EntryPath(install.Digest)
	if err != nil {
		return nil, err
	}
	root := install.ToolRoot(entry)

	// The profile's own executable paths are checked against the install
	// record, not merely resolved against the directory. The catalogue said
	// which files must be executable; the profile says which ones this action
	// runs; a file the profile names that the catalogue never vouched for is
	// exactly what this catches.
	relative := make([]string, 0, len(request.Executables))
	for _, declared := range request.Executables {
		relative = append(relative, expandExeSuffix(declared.File, platform))
	}
	if err := a.cache.VerifyPaths(install.Digest, relative); err != nil {
		return nil, err
	}
	result, err := a.resolveRoot(request, platform, root, profile.AcquireManagedDownload,
		fmt.Sprintf("downloaded from the Auto-Pigeon catalogue %s serial %d, signed by key %s, digest %s",
			install.CatalogID, install.CatalogSerial, install.Signer, install.Digest))
	if err != nil {
		return nil, err
	}
	result.Install = install
	return result, nil
}

// expandExeSuffix applies the one placeholder a profile's executable path may
// contain. See [profile.Executable].
func expandExeSuffix(file string, platform profile.Platform) string {
	return strings.ReplaceAll(file, "{platform.exe_suffix}", platform.ExeSuffix())
}

// checkExecutable refuses anything that is not a regular file that could be
// run. It does not check the executable bit on Windows, where there is none.
func checkExecutable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("acquire: %s is not there", path)
		}
		return fmt.Errorf("acquire: checking %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("acquire: %s is not a regular file", path)
	}
	if goos() != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("acquire: %s is not executable", path)
	}
	return nil
}

// formatBytes renders a size the way a person reads one.
func formatBytes(size int64) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(size)/float64(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(size)/float64(1<<10))
	}
	return fmt.Sprintf("%d bytes", size)
}
