package binding

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// SchemaVersion versions the local binding record.
//
// Separate from the profile schema version because the two have different
// compatibility obligations: a portable document has to be readable by other
// people's builds for a long time, and a local file only has to be readable by
// the next version of this program. Tying them together would force a migration
// of local state every time the published format moved.
const SchemaVersion = profile.LocalBindingSchemaVersion

// Override is a user's deliberate departure from what a profile or a discovery
// pass suggested.
//
// Recorded rather than merged, because "the Companion found this" and "I told
// it this" are different facts and the second must survive a re-scan. An
// override with no reason is still an override; the field is there because a
// user coming back in six months is the main reader.
type Override struct {
	// Kind is `executable`, `root` or `version`.
	Kind   string    `json:"kind"`
	Name   string    `json:"name"`
	Value  string    `json:"value"`
	Reason string    `json:"reason,omitempty"`
	SetAt  time.Time `json:"set_at"`
}

// PinnedInstall is LEGACY: a managed download a binding written before
// 2026-09-23 depended on. The Companion downloads nothing any more; the field is
// still read, because bindings.json is decoded strictly and an existing file
// must keep loading, and it is never written.
type PinnedInstall struct {
	PackageID string `json:"package_id"`
	Version   string `json:"version"`
	// Digest is the artifact's `sha256:<hex>`, which names the cache entry.
	Digest   string    `json:"digest"`
	Platform string    `json:"platform,omitempty"`
	PinnedAt time.Time `json:"pinned_at"`
}

// LocalBinding is one profile, as installed on this machine.
type LocalBinding struct {
	SchemaVersion string `json:"schema_version"`
	// ProfileID, ProfileVersion and ProfileDigest identify exactly which
	// document this binding belongs to. The digest is the load-bearing one: a
	// binding whose digest no longer matches the installed document describes
	// something that has changed since the user looked at it.
	ProfileID      string        `json:"profile_id"`
	ProfileVersion string        `json:"profile_version"`
	ProfileDigest  string        `json:"profile_digest"`
	Trust          profile.Trust `json:"trust"`
	// Acquisition is how the executables got here.
	Acquisition profile.AcquisitionMode `json:"acquisition"`
	// Executables maps a declared executable name to its absolute path here.
	Executables map[string]string `json:"executables,omitempty"`
	// Roots maps a root role to an absolute path here. `workspace` is not
	// stored: it is created per job and belongs to the executor.
	Roots map[string]string `json:"roots,omitempty"`
	// Arguments maps a declared executable name to the argument tokens this
	// user adds to every command that runs it (NEW_265). One list per
	// executable, so a qbsp flag never reaches vis or light. Machine state like
	// the paths above: the document — the profile a user exports — is never
	// changed by it, and a Companion update that ships a new document keeps it.
	Arguments map[string][]string `json:"arguments,omitempty"`
	// StepArguments maps a PIPELINE stage id to the argument tokens this user
	// adds to that stage's command (operator, 2026-10-03: "in build tools you
	// set up the paths and metadata of tools, in pipelines you pick a tool and
	// add the parameters"). Recorded on the pipeline's binding, never on the
	// tool's: two pipelines that share a compiler do not share its flags, and
	// one pipeline can run the same tool twice with different ones. Machine
	// state exactly as Arguments is — the document is not touched and an
	// export carries none of it.
	StepArguments map[string][]string `json:"step_arguments,omitempty"`
	// ResolvedVersion is what the version probe reported, and VersionCheckedAt
	// is when. Both, or neither: a version with no timestamp is a claim with no
	// expiry, and a tool updated in place would keep the old number forever.
	ResolvedVersion  string    `json:"resolved_version,omitempty"`
	VersionCheckedAt time.Time `json:"version_checked_at,omitempty"`
	// GameProfileID is AUB's record id for the Game Profile the document names
	// by slug. Resolving the slug is an account-and-deployment question, which
	// is why the answer lives here and the question lives in the document.
	GameProfileID string `json:"game_profile_id,omitempty"`
	// Installs is LEGACY and never written. See [PinnedInstall].
	Installs []PinnedInstall `json:"installs,omitempty"`
	// Grant is what the user approved, against ProfileDigest.
	Grant     *profile.Grant `json:"grant,omitempty"`
	Overrides []Override     `json:"overrides,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Validate checks a binding against the machine it claims to describe.
//
// The rules are the inverse of a profile's. Here a relative path is the fault:
// a binding is resolved against nothing, so a path that is not absolute is a
// path that depends on the Companion's working directory, and that is a
// different directory depending on how the user started it.
func (b LocalBinding) Validate() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if !profile.LocalBindingSchemaSupported(b.SchemaVersion) {
		add("schema_version is %q; this build reads %s", b.SchemaVersion,
			strings.Join(profile.SupportedLocalBindingSchemaVersions, " and "))
	}
	if b.ProfileID == "" {
		add("profile_id is empty")
	}
	switch {
	case b.ProfileDigest == "":
		add("profile_digest is empty; a binding with no digest cannot tell whether the document has changed since it was approved")
	case !strings.HasPrefix(b.ProfileDigest, "sha256:"):
		add("profile_digest %q does not name its algorithm", b.ProfileDigest)
	}
	if b.Trust != "" && !b.Trust.Valid() {
		add("trust is %q, which is not a trust state", b.Trust)
	}
	for _, name := range sortedKeys(b.Executables) {
		if !filepath.IsAbs(b.Executables[name]) {
			add("the path for the executable %q is %q, which is not absolute", name, b.Executables[name])
		}
	}
	for name, tokens := range b.Arguments {
		if strings.TrimSpace(name) == "" {
			add("arguments are recorded for an executable with no name")
			continue
		}
		if err := profile.ValidateCustomArgs(tokens); err != nil {
			add("the arguments for the executable %q: %v", name, err)
		}
	}
	for step, tokens := range b.StepArguments {
		if strings.TrimSpace(step) == "" {
			add("arguments are recorded for a pipeline stage with no id")
			continue
		}
		if err := profile.ValidateCustomArgs(tokens); err != nil {
			add("the arguments for the stage %q: %v", step, err)
		}
	}
	for _, role := range sortedKeys(b.Roots) {
		if role == profile.RootWorkspace {
			add("the %q root is created per job and must not be stored in a binding", profile.RootWorkspace)
			continue
		}
		if !filepath.IsAbs(b.Roots[role]) {
			add("the path for the %q root is %q, which is not absolute", role, b.Roots[role])
		}
	}
	if b.ResolvedVersion != "" && b.VersionCheckedAt.IsZero() {
		add("resolved_version is set but version_checked_at is not; a recorded version with no timestamp never goes stale")
	}
	seenInstall := map[string]bool{}
	for _, install := range b.Installs {
		switch {
		case install.PackageID == "":
			add("an install has no package id")
		case !strings.HasPrefix(install.Digest, "sha256:") || len(install.Digest) != len("sha256:")+64:
			add("the install of %q has the digest %q, which is not sha256:<64 hex digits>", install.PackageID, install.Digest)
		case seenInstall[install.Digest]:
			add("the install %s is recorded twice", install.Digest)
		}
		seenInstall[install.Digest] = true
	}
	if b.Grant != nil && b.Grant.Digest != b.ProfileDigest {
		add("the grant covers digest %q but the binding is for %q; the document changed after it was approved", b.Grant.Digest, b.ProfileDigest)
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("binding %s: %s", b.ProfileID, strings.Join(problems, "; "))
}

// Request builds the machine half of a profile resolution.
//
// The workspace is a parameter rather than a member: it belongs to one job, is
// created and destroyed by the executor, and storing it in a binding would make
// a stale path outlive the directory it names.
func (b LocalBinding) Request(platform profile.Platform, workspace string, inputs, options, runtime map[string]string) profile.Request {
	roots := make(map[string]string, len(b.Roots)+1)
	for role, path := range b.Roots {
		roots[role] = path
	}
	if workspace != "" {
		roots[profile.RootWorkspace] = workspace
	}
	executables := make(map[string]string, len(b.Executables))
	for name, path := range b.Executables {
		executables[name] = path
	}
	return profile.Request{
		Platform:    platform,
		Roots:       roots,
		Executables: executables,
		Inputs:      inputs,
		Options:     options,
		Runtime:     runtime,
	}
}

// Authorize is the gate every acquisition and every run passes through. It
// delegates to the profile package so that "may this run" has one answer.
func (b LocalBinding) Authorize(p profile.Profile) error {
	return profile.Authorize(p, b.Trust, b.ProfileDigest, b.Grant)
}

// Stale reports whether the recorded tool version is old enough to be worth
// probing again.
func (b LocalBinding) Stale(now time.Time, maxAge time.Duration) bool {
	if b.ResolvedVersion == "" || b.VersionCheckedAt.IsZero() {
		return true
	}
	return now.Sub(b.VersionCheckedAt) > maxAge
}

// Set is every binding on this machine, as stored.
type Set struct {
	SchemaVersion string         `json:"schema_version"`
	Bindings      []LocalBinding `json:"bindings"`
}

// NewSet returns an empty set stamped with the current schema version.
func NewSet() *Set { return &Set{SchemaVersion: SchemaVersion} }

// Find returns the binding for a profile id.
func (s *Set) Find(profileID string) (LocalBinding, bool) {
	for _, b := range s.Bindings {
		if b.ProfileID == profileID {
			return b, true
		}
	}
	return LocalBinding{}, false
}

// Put inserts or replaces a binding, stamping it with the current format. A
// binding read at an older version is written back at this one, which is what
// keeps the supported-version list from growing without bound.
func (s *Set) Put(b LocalBinding) error {
	b.SchemaVersion = SchemaVersion
	if b.UpdatedAt.IsZero() {
		b.UpdatedAt = time.Now().UTC()
	}
	if err := b.Validate(); err != nil {
		return err
	}
	for i := range s.Bindings {
		if s.Bindings[i].ProfileID == b.ProfileID {
			s.Bindings[i] = b
			return nil
		}
	}
	s.Bindings = append(s.Bindings, b)
	sort.Slice(s.Bindings, func(i, j int) bool { return s.Bindings[i].ProfileID < s.Bindings[j].ProfileID })
	return nil
}

// Remove drops a binding.
func (s *Set) Remove(profileID string) bool {
	for i := range s.Bindings {
		if s.Bindings[i].ProfileID == profileID {
			s.Bindings = append(s.Bindings[:i], s.Bindings[i+1:]...)
			return true
		}
	}
	return false
}

// Marshal renders the set for storage, indented because a user is entitled to
// read and repair a file that records what they approved.
func (s *Set) Marshal() ([]byte, error) {
	s.SchemaVersion = SchemaVersion
	for i := range s.Bindings {
		s.Bindings[i].SchemaVersion = SchemaVersion
	}
	return json.MarshalIndent(s, "", "  ")
}

// Load reads a stored set, refusing unknown members: a binding file this build
// does not fully understand is one whose grants it cannot honestly enforce.
func Load(data []byte) (*Set, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var s Set
	if err := decoder.Decode(&s); err != nil {
		return nil, fmt.Errorf("binding: reading the binding store: %w", err)
	}
	if !profile.LocalBindingSchemaSupported(s.SchemaVersion) {
		return nil, fmt.Errorf("binding: the binding store is %q; this build reads %s", s.SchemaVersion,
			strings.Join(profile.SupportedLocalBindingSchemaVersions, " and "))
	}
	for _, b := range s.Bindings {
		if err := b.Validate(); err != nil {
			return nil, err
		}
	}
	return &s, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
