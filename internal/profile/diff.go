package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// The normalized diff a user reviews before an update is applied.
//
// # Why the diff is over the canonical form
//
// Comparing two documents as text would report a reordered member, a
// reindented block and a changed comment as changes, and would report a
// reordered *array* as no change at all if the text happened to match. Both
// mistakes have the same effect on a reviewer: they teach them that the diff is
// noise, and a diff a user has learned to skip is a review that does not
// happen.
//
// So both sides are canonicalized first. What comes out is a change list where
// every entry is a difference in what the document *says*, addressed by a path
// the error messages use too.
//
// # And why permissions are separated out
//
// A change to `summary` and a change to `network.required` are not the same
// kind of event, and a flat list makes the second look like the first. The
// permission delta is computed independently and is what
// [Diff.Escalates] answers, because that is the question the update flow turns
// on: an update that widens what a profile may do needs the user's decision
// again, and an update that does not can be applied with the diff on record.

// ChangeKind is what happened at a path.
type ChangeKind string

const (
	Added   ChangeKind = "added"
	Removed ChangeKind = "removed"
	Changed ChangeKind = "changed"
)

// Change is one difference.
type Change struct {
	Path   string     `json:"path"`
	Kind   ChangeKind `json:"kind"`
	Before string     `json:"before,omitempty"`
	After  string     `json:"after,omitempty"`
}

func (c Change) String() string {
	switch c.Kind {
	case Added:
		return "+ " + c.Path + " = " + c.After
	case Removed:
		return "- " + c.Path + " was " + c.Before
	}
	return "~ " + c.Path + ": " + c.Before + " -> " + c.After
}

// Diff is the reviewable difference between two versions of a profile.
type Diff struct {
	// FirstImport is set when there is no previous version: the whole document
	// is new, so a change list would just be the document again.
	FirstImport bool `json:"first_import"`
	// IdentityChanged is set when the id changed, which means this is not an
	// update at all and should not be presented as one.
	IdentityChanged bool `json:"identity_changed"`
	// VersionRepublished is set when the id and the version are the same and
	// the bytes are not. See [ErrRepublished]: this is the one difference that
	// is a fault rather than a change.
	VersionRepublished bool     `json:"version_republished"`
	Changes            []Change `json:"changes"`
	// PermissionsAdded is what the new version asks for and the old did not.
	PermissionsAdded []Permission `json:"permissions_added"`
	// PermissionsRemoved is what it no longer asks for.
	PermissionsRemoved []Permission `json:"permissions_removed"`
}

// Escalates reports whether the incoming document needs the user's decision
// again: it asks for something new, it is not the profile that was installed,
// or it is the same version saying something different.
func (d Diff) Escalates() bool {
	return len(d.PermissionsAdded) > 0 || d.IdentityChanged || d.VersionRepublished
}

// Empty reports a diff with nothing in it.
func (d Diff) Empty() bool {
	return !d.FirstImport && !d.IdentityChanged && !d.VersionRepublished && len(d.Changes) == 0 &&
		len(d.PermissionsAdded) == 0 && len(d.PermissionsRemoved) == 0
}

// DiffProfiles compares an installed profile with an incoming one. `before` is
// nil for a first import.
func DiffProfiles(before, after Profile) (Diff, error) {
	var d Diff
	if after == nil {
		return d, fmt.Errorf("profile: nothing to diff against")
	}
	newPermissions := after.Permissions()
	if before == nil {
		d.FirstImport = true
		d.PermissionsAdded = newPermissions
		return d, nil
	}
	if before.Metadata().ID != after.Metadata().ID {
		d.IdentityChanged = true
	}
	if err := CheckVersionImmutable(before, after); err != nil {
		d.VersionRepublished = true
	}

	oldTree, err := canonicalTree(before)
	if err != nil {
		return d, err
	}
	newTree, err := canonicalTree(after)
	if err != nil {
		return d, err
	}
	c := &changeWalk{}
	c.walk("", oldTree, newTree)
	d.Changes = c.changes

	oldPermissions := map[string]Permission{}
	for _, p := range before.Permissions() {
		oldPermissions[p.ID] = p
	}
	seen := map[string]bool{}
	for _, p := range newPermissions {
		seen[p.ID] = true
		if _, had := oldPermissions[p.ID]; !had {
			d.PermissionsAdded = append(d.PermissionsAdded, p)
		}
	}
	for _, id := range sortedKeys(oldPermissions) {
		if !seen[id] {
			d.PermissionsRemoved = append(d.PermissionsRemoved, oldPermissions[id])
		}
	}
	return d, nil
}

// canonicalTree renders a profile through the canonical encoder and back, so
// the diff walks exactly the bytes a digest would cover.
//
// It uses the unchecked encoder: a document that fails the portability rules
// still has to be *showable*, because "here is what was wrong with it" is the
// only useful thing to say about a refused import.
func canonicalTree(p Profile) (any, error) {
	data, err := canonicalUnchecked(p)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	var tree any
	if err := decoder.Decode(&tree); err != nil {
		return nil, err
	}
	return tree, nil
}

type changeWalk struct{ changes []Change }

func (w *changeWalk) add(path string, kind ChangeKind, before, after any) {
	// The document root has no name, so the separator that would precede the
	// first step is dropped — the same trimming the validation paths do, so a
	// change and a problem about the same member read the same.
	w.changes = append(w.changes, Change{
		Path:   strings.TrimPrefix(path, "."),
		Kind:   kind,
		Before: render(before),
		After:  render(after),
	})
}

func (w *changeWalk) walk(path string, before, after any) {
	switch b := before.(type) {
	case map[string]any:
		a, ok := after.(map[string]any)
		if !ok {
			w.add(path, Changed, before, after)
			return
		}
		names := map[string]bool{}
		for k := range b {
			names[k] = true
		}
		for k := range a {
			names[k] = true
		}
		keys := make([]string, 0, len(names))
		for k := range names {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			bv, hadB := b[k]
			av, hadA := a[k]
			child := path + field(k)
			switch {
			case hadB && !hadA:
				w.add(child, Removed, bv, nil)
			case !hadB && hadA:
				w.add(child, Added, nil, av)
			default:
				w.walk(child, bv, av)
			}
		}
	case []any:
		a, ok := after.([]any)
		if !ok {
			w.add(path, Changed, before, after)
			return
		}
		// Positional, not set-based. An argument array's order is its meaning:
		// `-threads 4` and `4 -threads` are different commands, and a diff that
		// called them equal would be hiding the only thing that mattered.
		for i := 0; i < len(b) || i < len(a); i++ {
			child := path + index(i)
			switch {
			case i >= len(a):
				w.add(child, Removed, b[i], nil)
			case i >= len(b):
				w.add(child, Added, nil, a[i])
			default:
				w.walk(child, b[i], a[i])
			}
		}
	default:
		if render(before) != render(after) {
			w.add(path, Changed, before, after)
		}
	}
}

// render is the compact one-line spelling of a value in a change list.
//
// One line, and short. A change list is read by somebody deciding whether to
// accept an update, and a four-paragraph description rendered in full pushes
// the two lines that matter — a new network member, a new writable root — off
// the screen. The full text is in the document; this is the index to it.
func render(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return `"` + clip(oneLine(value)) + `"`
	case json.Number:
		return value.String()
	case bool:
		if value {
			return "true"
		}
		return "false"
	}
	data, err := canonicalUnchecked(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return clip(string(data))
}

// renderLimit is generous enough to show a whole argument array and short
// enough that no single change can fill a screen.
const renderLimit = 160

func clip(s string) string {
	if len(s) <= renderLimit {
		return s
	}
	// Cut on a rune boundary: a change list that prints half a character is a
	// change list somebody stops trusting.
	cut := renderLimit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

// String renders the diff for a human review.
func (d Diff) String() string {
	var b strings.Builder
	switch {
	case d.FirstImport:
		b.WriteString("This profile is new to this machine.\n")
	case d.IdentityChanged:
		b.WriteString("This document has a different id from the one installed; it is a different profile, not an update.\n")
	case d.VersionRepublished:
		b.WriteString("This document has the same id and version as the one installed, and says something different. " +
			"A published version is immutable: whatever changed should have been a new version.\n")
	case d.Empty():
		b.WriteString("No change.\n")
	}
	if len(d.Changes) > 0 {
		fmt.Fprintf(&b, "\nChanges (%d):\n", len(d.Changes))
		for _, c := range d.Changes {
			b.WriteString("  " + c.String() + "\n")
		}
	}
	if len(d.PermissionsAdded) > 0 {
		b.WriteString("\nIt now asks to:\n")
		for _, p := range d.PermissionsAdded {
			fmt.Fprintf(&b, "  - %s [%s]\n", p.Summary, p.Risk)
		}
	}
	if len(d.PermissionsRemoved) > 0 {
		b.WriteString("\nIt no longer asks to:\n")
		for _, p := range d.PermissionsRemoved {
			fmt.Fprintf(&b, "  - %s\n", p.Summary)
		}
	}
	return b.String()
}

// Immutability of a published version.
//
// A version number is the handle everything else uses: a catalogue entry, a
// pinned dependency, a changelog, a person saying "I am on 0.3.0". If the bytes
// behind that handle can change, then every one of those references means
// something different at different times, and — the case that matters here —
// a profile a user reviewed and approved becomes a different program without
// the version they approved ever changing.
//
// The digest is what makes this detectable at all, and grant-by-digest is what
// makes it *refused* rather than merely noticed. This function is the sentence
// that goes with the refusal.

// RepublishedError reports that a version was changed rather than superseded.
type RepublishedError struct {
	ProfileID string
	Version   string
	Installed string
	Incoming  string
}

func (e *RepublishedError) Error() string {
	return fmt.Sprintf(
		"profile: %s %s is already installed with a different content (%s, now %s); "+
			"a published version is immutable, so whatever changed should have been a new version",
		e.ProfileID, e.Version, e.Installed, e.Incoming)
}

// CheckVersionImmutable reports whether an incoming document is a republication
// of a version that is already installed: the same id and the same version,
// saying something different.
//
// It is not an error for the digests to differ when the versions differ — that
// is an ordinary update — and not an error for two different profiles to share
// a version number.
func CheckVersionImmutable(installed, incoming Profile) error {
	if installed == nil || incoming == nil {
		return nil
	}
	before, after := installed.Metadata(), incoming.Metadata()
	if before.ID != after.ID || before.Version != after.Version {
		return nil
	}
	beforeBytes, err := canonicalUnchecked(installed)
	if err != nil {
		return err
	}
	afterBytes, err := canonicalUnchecked(incoming)
	if err != nil {
		return err
	}
	if bytes.Equal(beforeBytes, afterBytes) {
		return nil
	}
	return &RepublishedError{
		ProfileID: before.ID,
		Version:   before.Version,
		Installed: DigestBytes(beforeBytes),
		Incoming:  DigestBytes(afterBytes),
	}
}
