package profile

import (
	"fmt"
	"strings"
	"time"
)

// Trust: who vouches for a document, and what the user had to agree to.
//
// # The four states, and the one that surprises people
//
// Trust here answers exactly one question — *who says this document is what it
// claims to be* — and the four answers are: the release you installed, the
// signed catalogue, nobody, and nobody.
//
// The surprise is that `local` is one of the two "nobody"s. It reads like the
// friendly state — the user wrote it themselves, on their own machine, so what
// is there to distrust — and that reading is what makes it dangerous. "Local"
// is a statement about a file's *location*, and a file's location is the
// easiest property in the system for something else to arrange. A profile
// dropped into the profile directory by an installer, a sync client, an
// archive extracted from a download or another program on the same account
// presents as `local` and nothing has vouched for it. So `local` requires a
// grant exactly as `community` does, and neither is treated as safer than the
// other anywhere in this package.
//
// # What trust does not do
//
// It does not decide what a profile may ask for. A built-in profile that wants
// to write into a game directory declares that root exactly as a community one
// does, and the permission appears in the summary either way. Trust decides
// whether a grant has to be collected and how the document's provenance is
// described — never what the document is permitted to say.

// Trust is a profile's provenance state.
type Trust string

const (
	// TrustBuiltin is compiled into this build of the Companion. It arrived
	// with the program, so trusting it is the same act as trusting the program.
	TrustBuiltin Trust = "builtin"
	// TrustVerified is signed by an Auto-Pigeon catalogue key whose signature
	// covers this document's exact digest.
	TrustVerified Trust = "verified"
	// TrustCommunity came from somewhere else. It may be excellent; nothing
	// here knows.
	TrustCommunity Trust = "community"
	// TrustLocal was written or edited on this machine. See the package
	// comment above: this is not a synonym for "safe".
	TrustLocal Trust = "local"
)

// TrustStates is every state, in the order they are documented.
var TrustStates = []Trust{TrustBuiltin, TrustVerified, TrustCommunity, TrustLocal}

// Valid reports whether a string is a trust state.
func (t Trust) Valid() bool {
	for _, s := range TrustStates {
		if s == t {
			return true
		}
	}
	return false
}

// Vouched reports whether anybody stands behind the document. It is the
// distinction that matters, and it puts `local` on the same side as
// `community`.
func (t Trust) Vouched() bool { return t == TrustBuiltin || t == TrustVerified }

// Describe is the sentence shown next to a profile in a list.
func (t Trust) Describe() string {
	switch t {
	case TrustBuiltin:
		return "Built in — shipped with this version of the Companion."
	case TrustVerified:
		return "Verified — signed by the Auto-Pigeon catalogue."
	case TrustCommunity:
		return "Community — imported from elsewhere; nobody has checked it for you."
	case TrustLocal:
		return "Local — found in your profile folder; nobody has checked it for you, and the Companion cannot tell who put it there."
	}
	return "Unknown provenance."
}

// Event is something that happens to a profile and can change its trust.
type Event string

const (
	// EventInstallBuiltin: the Companion loaded one of its own embedded
	// documents.
	EventInstallBuiltin Event = "install_builtin"
	// EventImportSigned: imported with a catalogue signature that verified
	// against this exact digest.
	EventImportSigned Event = "import_signed"
	// EventImportUnsigned: imported from a file, a URL, a paste.
	EventImportUnsigned Event = "import_unsigned"
	// EventAuthorLocally: created in the Companion, or found in the local
	// profile folder.
	EventAuthorLocally Event = "author_locally"
	// EventEdit: the document changed on this machine.
	EventEdit Event = "edit"
	// EventFork: copied under a new id, leaving the original alone.
	EventFork Event = "fork"
	// EventSignatureVerified: a catalogue signature now covers this digest.
	EventSignatureVerified Event = "signature_verified"
	// EventSignatureLost: the signature no longer verifies — the key was
	// revoked, the catalogue expired, the entry was withdrawn.
	EventSignatureLost Event = "signature_lost"
)

// TrustError explains a refused transition.
type TrustError struct {
	From   Trust
	Event  Event
	Reason string
}

func (e *TrustError) Error() string {
	from := string(e.From)
	if from == "" {
		from = "a profile that is not installed"
	}
	return fmt.Sprintf("profile: %s cannot %s: %s", from, e.Event, e.Reason)
}

// Transition applies an event to a trust state.
//
// `from` is empty for a profile that is not installed yet.
//
// Two rules here are worth stating in prose because they are the ones a future
// change is most likely to soften:
//
//   - **Editing drops a profile to `local`, whatever it was.** A verified
//     document is verified because a signature covers its bytes; changing the
//     bytes does not produce a differently-signed document, it produces an
//     unsigned one. Keeping the badge across an edit would let anything that
//     can write to a file inherit the catalogue's word.
//   - **Nothing is promoted to `builtin`, ever.** Built-in means "arrived with
//     the program", which is a fact about the release and not a status a
//     document can earn at runtime. Editing a built-in profile forks it: the
//     original stays in the build, the copy is `local` under its own id.
func Transition(from Trust, event Event) (Trust, error) {
	if from != "" && !from.Valid() {
		return "", &TrustError{From: from, Event: event, Reason: fmt.Sprintf("%q is not a trust state", from)}
	}
	if from == TrustBuiltin {
		switch event {
		case EventFork:
			return TrustLocal, nil
		case EventInstallBuiltin:
			return TrustBuiltin, nil
		default:
			return "", &TrustError{From: from, Event: event,
				Reason: "a built-in profile is part of the build and cannot be changed or re-provenanced at run time; fork it instead"}
		}
	}
	switch event {
	case EventInstallBuiltin:
		if from != "" {
			return "", &TrustError{From: from, Event: event,
				Reason: "a profile that is already installed cannot become built in; built-in means it arrived with the program"}
		}
		return TrustBuiltin, nil
	case EventImportSigned, EventSignatureVerified:
		return TrustVerified, nil
	case EventImportUnsigned:
		return TrustCommunity, nil
	case EventAuthorLocally, EventEdit:
		return TrustLocal, nil
	case EventFork:
		return TrustLocal, nil
	case EventSignatureLost:
		if from != TrustVerified {
			return "", &TrustError{From: from, Event: event, Reason: "only a verified profile has a signature to lose"}
		}
		return TrustCommunity, nil
	}
	return "", &TrustError{From: from, Event: event, Reason: "unknown event"}
}

// Grant is a user's recorded decision about one exact document.
//
// It is keyed by digest, not by id and version. A version number is a claim the
// author makes; a digest is a fact about the bytes. Granting by version would
// mean that anything able to rewrite a file in place — including the author,
// after the fact — could change what was approved without the approval
// changing.
type Grant struct {
	ProfileID string    `json:"profile_id"`
	Version   string    `json:"version"`
	Digest    string    `json:"digest"`
	Trust     Trust     `json:"trust"`
	Granted   []string  `json:"granted"`
	GrantedAt time.Time `json:"granted_at"`
}

// Covers reports whether a grant applies to a document.
func (g *Grant) Covers(id, digest string) bool {
	return g != nil && g.ProfileID == id && g.Digest == digest
}

// NotGrantedError reports that a profile may not run yet, and says what is
// missing rather than that something is.
type NotGrantedError struct {
	ProfileID string
	Trust     Trust
	// Reason is `not_reviewed`, `document_changed` or `permissions_widened`.
	Reason string
	// Missing is the permissions that have not been granted. For a widened
	// document this is only the new ones, which is what makes the second review
	// short enough to be read.
	Missing []Permission
}

func (e *NotGrantedError) Error() string {
	var b strings.Builder
	switch e.Reason {
	case "document_changed":
		b.WriteString("profile: " + e.ProfileID + " has changed since it was approved")
	case "permissions_widened":
		b.WriteString("profile: " + e.ProfileID + " now asks for more than was approved")
	default:
		b.WriteString("profile: " + e.ProfileID + " has not been reviewed yet")
	}
	if len(e.Missing) > 0 {
		b.WriteString("; it needs:")
		for _, p := range e.Missing {
			b.WriteString("\n  - " + p.Summary + " [" + string(p.Risk) + "]")
		}
	}
	return b.String()
}

// Authorize decides whether a profile may be acted on: acquired, resolved, run.
//
// Importing is inert, and this function is where that is true. A document can
// be fetched, parsed, validated, canonicalized, digested, diffed and displayed
// with no grant at all — none of that touches the network on the profile's
// behalf or starts a process. Everything that does goes through here first.
func Authorize(p Profile, trust Trust, digest string, grant *Grant) error {
	meta := p.Metadata()
	required := p.Permissions()

	// A built-in document is authorized by having been installed: the user's
	// decision was to install this build. It is still identified by digest,
	// because the loader compares what it read against what was compiled in.
	if trust == TrustBuiltin {
		return nil
	}
	if grant == nil {
		return &NotGrantedError{ProfileID: meta.ID, Trust: trust, Reason: "not_reviewed", Missing: required}
	}
	if !grant.Covers(meta.ID, digest) {
		return &NotGrantedError{ProfileID: meta.ID, Trust: trust, Reason: "document_changed", Missing: required}
	}
	granted := map[string]bool{}
	for _, id := range grant.Granted {
		granted[id] = true
	}
	var missing []Permission
	for _, p := range required {
		if !granted[p.ID] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		return &NotGrantedError{ProfileID: meta.ID, Trust: trust, Reason: "permissions_widened", Missing: missing}
	}
	return nil
}

// PermissionReport renders a profile's permissions for a human to read before
// approving them.
//
// Ordered high risk first, one line each, second person, no jargon. A review
// screen is only worth having if it is short enough that somebody reads it, so
// the ordering is doing real work: whatever is at the top is what a user who
// reads two lines will see.
func PermissionReport(p Profile, trust Trust) string {
	meta := p.Metadata()
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s (%s)\n", meta.Name, meta.Version, meta.ID)
	fmt.Fprintf(&b, "  published by %s, under %s\n", meta.Publisher.Name, meta.License.SPDX)
	fmt.Fprintf(&b, "  %s\n", trust.Describe())
	permissions := p.Permissions()
	if len(permissions) == 0 {
		b.WriteString("\n  It asks for nothing on its own account.\n")
		return b.String()
	}
	b.WriteString("\n  If you approve it, it may:\n")
	for _, permission := range permissions {
		fmt.Fprintf(&b, "    - %s [%s]\n", permission.Summary, permission.Risk)
	}
	return b.String()
}

// PermissionIDs is the grant list a user's approval produces.
func PermissionIDs(p Profile) []string {
	permissions := p.Permissions()
	ids := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		ids = append(ids, permission.ID)
	}
	return ids
}
