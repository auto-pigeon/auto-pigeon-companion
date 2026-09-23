// Package approval records and withdraws a user's decision about one exact
// profile document.
//
// # Why this is a package and not two handlers
//
// Until AUCOM/AUT 228 there were three ways to write a [profile.Grant] and no
// two of them agreed. `companion engine bind --approve` did it for engine
// profiles only. `companion profile install --approve` did it for a document
// arriving from a deployment listing. `POST /api/v1/profiles/{id}/grant` did it
// for any kind, which meant a tool profile a person had written themselves — the
// document this program most expects a user to author — could be validated,
// shown, digested and bound from the command line, and then only ever RUN by
// starting the local server and using the HTTP API. AUT/AUCOM 219 filed that as
// defect 6, against the README's own invariant: everything the page can do, the
// CLI can do too, through the same services.
//
// "Through the same services" is the part that needed code rather than a second
// implementation. A grant is the most consequential thing this program writes:
// it is the difference between a document that is data and a document that is
// allowed to start a process. Two call sites that each assemble a
// [binding.LocalBinding] and each remember to check the digest are two call
// sites that will one day differ, and the one that drifts will be the one that
// forgot a check. So there is one [Service], the CLI and the web server both
// hold one, and neither of them knows how a grant is stored.
//
// # What it will not do
//
//   - It will not grant without a digest, and not against a digest that is not
//     the document on this machine right now. An approval is an approval of
//     bytes somebody read; a stale digest means they read something else. See
//     [StaleDigestError], which names both.
//   - It will not decide anything on the caller's behalf. [Service.Review] is
//     inert — it reads, reports and returns — and a caller that wants to grant
//     has to say so in a second call. Reviewing is not approving, and a service
//     whose read path could write would make "show me what this asks for" a
//     dangerous thing to type.
//   - It will not touch a profile document, an executable path or a root. A
//     grant is a decision, and where a program lives on this machine is not part
//     of what was decided — which is why [Service.Withdraw] leaves the paths
//     alone.
//
// # Writing is locked, because two instances is the normal case
//
// The GUI server is one process and a `companion` invocation in a terminal is a
// second. Both may hold the binding file open across a decision. Every write
// here goes through [binding.Update], which does the read-modify-write inside a
// cross-process lock, so a grant recorded in one window cannot silently discard
// an engine path recorded in the other. See internal/lockfile.
package approval

import (
	"errors"
	"fmt"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// ErrDigestRequired reports that an approval arrived without naming what it
// approves.
//
// Not defaulted to "whatever is on disk". A caller that did not send a digest is
// a caller that did not read one, and an approval of a document nobody looked at
// is the failure this whole mechanism exists to stop.
var ErrDigestRequired = errors.New(
	"approval: an approval names the exact document being approved: give its digest")

// StaleDigestError reports that the document changed between the review and the
// approval.
//
// Both digests are named. "This changed" is not actionable; "you approved A and
// this machine now has B" tells a person exactly what to look at, and the
// message says what to do about it.
type StaleDigestError struct {
	ProfileID string
	// Reviewed is the digest the caller supplied, and Current is the document
	// on this machine now.
	Reviewed string
	Current  string
}

func (e *StaleDigestError) Error() string {
	return fmt.Sprintf(
		"approval: this approval is for %s and the document on this machine is now %s; "+
			"read %s again before approving it", e.Reviewed, e.Current, e.ProfileID)
}

// Service is the one writer of a profile grant on this machine.
//
// Catalog is where a profile id is resolved, and it is deliberately the caller's
// — the executor's chain in both the CLI and the server, so that the document
// approved here is the document that would run there. A service with a catalog
// of its own would be a service that could approve something the executor
// cannot see, or miss something it can.
type Service struct {
	Catalog job.Catalog
	// BindingsPath is the binding store this machine's grants live in.
	BindingsPath string
	// Now supplies the clock, for tests. Nil means time.Now.
	Now func() time.Time
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Decision is one profile as this machine currently stands on it: the document,
// the local binding, the permission report a person reads before approving, and
// [profile.Authorize]'s own verdict.
//
// Authorized is Authorize's answer rather than something assembled from trust
// and grant by whoever is rendering it. A caller that has to compute "may this
// run" from three fields is a caller that will one day compute it wrong.
type Decision struct {
	Entry   job.CatalogEntry
	Binding binding.LocalBinding
	// Report is the normalized permission report, ordered high risk first. It is
	// what the CLI prints before an approval and what the page renders.
	Report string
	// Authorized is whether this profile may be acted on as things stand.
	Authorized bool
	// AuthorizationError is why not, when it may not. A [profile.NotGrantedError]
	// names which permissions are missing and why.
	AuthorizationError error
}

// Review reads one profile and reports where it stands. It writes nothing.
func (s Service) Review(id string) (Decision, error) {
	entry, err := s.Catalog.Lookup(id)
	if err != nil {
		return Decision{}, err
	}
	local, err := s.find(entry.Profile.Metadata().ID)
	if err != nil {
		return Decision{}, err
	}
	return s.decide(entry, local), nil
}

// Grant records that a user approved everything a profile asks for, against one
// exact digest.
//
// The digest is required and must be the document on this machine. Everything
// else about the binding is left as it was: a grant says what somebody decided,
// not where their programs are.
func (s Service) Grant(id, digest string) (Decision, error) {
	entry, err := s.Catalog.Lookup(id)
	if err != nil {
		return Decision{}, err
	}
	if digest == "" {
		return Decision{}, ErrDigestRequired
	}
	meta := entry.Profile.Metadata()
	if digest != entry.Digest {
		return Decision{}, &StaleDigestError{ProfileID: meta.ID, Reviewed: digest, Current: entry.Digest}
	}

	var stored binding.LocalBinding
	set, err := binding.Update(s.BindingsPath, func(set *binding.Set) error {
		local, _ := set.Find(meta.ID)
		local.ProfileID = meta.ID
		local.ProfileVersion = meta.Version
		local.ProfileDigest = entry.Digest
		local.Trust = entry.Trust
		if local.Acquisition == "" {
			// The document is here and nobody downloaded it against the
			// catalogue, which is what `user_path` means. Recorded rather than
			// left empty so a binding written by an approval is the same shape
			// as one written by a bind.
			local.Acquisition = profile.AcquireUserPath
		}
		local.Grant = profile.NewGrant(entry.Profile, entry.Trust, entry.Digest, s.now())
		local.UpdatedAt = s.now().UTC()
		stored = local
		return set.Put(local)
	})
	if err != nil {
		return Decision{}, err
	}
	if found, ok := set.Find(meta.ID); ok {
		stored = found
	}
	return s.decide(entry, stored), nil
}

// Withdraw takes an approval back.
//
// The paths stay. Where a program is on this machine is not part of what was
// approved, and dropping it would mean withdrawing an approval also forgot an
// engine location the user set months ago — so taking the decision back would
// cost them the setup, and people who fear that do not take decisions back.
//
// Withdrawing when there is nothing to withdraw is not an error: the caller
// asked for a state and that is the state.
func (s Service) Withdraw(id string) (Decision, error) {
	entry, err := s.Catalog.Lookup(id)
	if err != nil {
		return Decision{}, err
	}
	meta := entry.Profile.Metadata()

	var stored binding.LocalBinding
	_, err = binding.Update(s.BindingsPath, func(set *binding.Set) error {
		local, found := set.Find(meta.ID)
		if !found || local.Grant == nil {
			stored = local
			return nil
		}
		local.Grant = nil
		local.UpdatedAt = s.now().UTC()
		stored = local
		return set.Put(local)
	})
	if err != nil {
		return Decision{}, err
	}
	return s.decide(entry, stored), nil
}

// find reads this machine's binding for a profile id. A machine with no binding
// file yet has none, and that is a first run rather than a failure.
func (s Service) find(id string) (binding.LocalBinding, error) {
	set, err := binding.LoadFile(s.BindingsPath)
	if err != nil && !errors.Is(err, binding.ErrNoFile) {
		return binding.LocalBinding{}, err
	}
	local, _ := set.Find(id)
	return local, nil
}

func (s Service) decide(entry job.CatalogEntry, local binding.LocalBinding) Decision {
	err := profile.Authorize(entry.Profile, entry.Trust, entry.Digest, local.Grant)
	return Decision{
		Entry:              entry,
		Binding:            local,
		Report:             profile.ProfileReport(entry.Profile, entry.Trust),
		Authorized:         err == nil,
		AuthorizationError: err,
	}
}
