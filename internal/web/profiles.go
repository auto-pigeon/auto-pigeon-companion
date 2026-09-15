package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/approval"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile/builtin"
)

// The Profiles area: reading a document, writing one, and approving one.
//
// # Why the wizard composes documents on this side
//
// A profile is a schema with rules the browser would otherwise have to
// reimplement — required members, portability (no absolute path, no host, no
// credential), a closed action vocabulary, template references that must
// resolve. A JavaScript form that built its own JSON would be a second
// implementation of those rules, and the two would agree until the day they did
// not. So the form posts FIELDS, this side applies them to a document, and the
// answer that comes back is the real document, run through the real decoder and
// the real validator. What the page draws is then a report about a document
// that exists, not a prediction about one it hopes to produce.
//
// # And why it starts from a template
//
// A blank engine profile is not something a person can fill in: it needs a
// game-profile reference, a content layout, an argv per action, and diagnostic
// rules. Every built-in document is a working example of all of that, so the
// wizard starts from one and changes what is genuinely this user's — the
// name, the id, the executable, which actions their engine supports. Nothing
// about that is a shortcut around the schema: the composed document is
// validated exactly as a pasted one is, and the normalized diff against the
// template is the thing that shows the user what they actually changed.
//
// # Approval is not a checkbox on a form
//
// [profile.Authorize] refuses anything not vouched for without a grant against
// one exact digest, and this API cannot bypass it: importing a document does
// not grant it, and the grant route refuses a digest that is not the document
// on disk. That is what makes "the trust review cannot be skipped" a property
// of the code rather than of the page's flow.

func (s *Server) profileAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/profiles":                s.handleProfileList,
		"GET /api/v1/profiles/templates":      s.handleProfileTemplates,
		"GET /api/v1/profiles/{id}":           s.handleProfileGet,
		"GET /api/v1/profiles/{id}/document":  s.handleProfileDocument,
		"POST /api/v1/profiles/validate":      s.handleProfileValidate,
		"POST /api/v1/profiles/compose":       s.handleProfileCompose,
		"POST /api/v1/profiles/diff":          s.handleProfileDiff,
		"POST /api/v1/profiles/import":        s.handleProfileImport,
		"POST /api/v1/profiles/{id}/bind":     s.handleProfileBind,
		"POST /api/v1/profiles/{id}/unbind":   s.handleProfileUnbind,
		"POST /api/v1/profiles/{id}/grant":    s.handleProfileGrant,
		"POST /api/v1/profiles/{id}/withdraw": s.handleProfileWithdraw,
		"POST /api/v1/profiles/{id}/remove":   s.handleProfileRemove,
	}
}

func (s *Server) handleProfileList(w http.ResponseWriter, r *http.Request) {
	catalog, err := s.catalog()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	entries, err := catalog.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	set, _, err := s.bindings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	kind := r.URL.Query().Get("kind")
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if kind != "" && string(entry.Profile.Metadata().Kind) != kind {
			continue
		}
		local, _ := set.Find(entry.Profile.Metadata().ID)
		items = append(items, s.describeCatalogEntry(entry, local))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleProfileGet(w http.ResponseWriter, r *http.Request) {
	entry, local, err := s.profileEntry(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.describeCatalogEntry(entry, local))
}

func (s *Server) profileEntry(id string) (job.CatalogEntry, binding.LocalBinding, error) {
	catalog, err := s.catalog()
	if err != nil {
		return job.CatalogEntry{}, binding.LocalBinding{}, err
	}
	entry, err := catalog.Lookup(id)
	if err != nil {
		return job.CatalogEntry{}, binding.LocalBinding{}, err
	}
	set, _, err := s.bindings()
	if err != nil {
		return entry, binding.LocalBinding{}, err
	}
	local, _ := set.Find(id)
	return entry, local, nil
}

// describeCatalogEntry is one profile as the Profiles area shows it.
//
// The portable half and the local half are separate members and always both
// present, so a page cannot accidentally render one as the other. `authorized`
// is [profile.Authorize]'s own answer rather than a guess assembled from trust
// and grant: a caller that has to compute "may this run" from three fields is a
// caller that will one day compute it wrong.
func (s *Server) describeCatalogEntry(entry job.CatalogEntry, local binding.LocalBinding) map[string]any {
	body := describeProfile(entry)
	// The declared programs, so a setup form can offer one field per program
	// rather than assuming there is exactly one. A pipeline declares none.
	body["executables"] = describeExecutables(entry.Profile)
	body["trust_description"] = entry.Trust.Describe()
	body["vouched"] = entry.Trust.Vouched()
	body["report"] = profile.PermissionReport(entry.Profile, entry.Trust)
	body["binding"] = describeBinding(local)
	body["editable"] = strings.HasPrefix(entry.Source, s.profilesPrefix())

	err := profile.Authorize(entry.Profile, entry.Trust, entry.Digest, local.Grant)
	body["authorized"] = err == nil
	if err != nil {
		body["authorization_error"] = err.Error()
	}
	return body
}

// profilesPrefix is the directory a document has to be under to be one this
// machine's user may edit or delete. A built-in document is inside the binary
// and has no path at all, which is what makes this test the right one.
func (s *Server) profilesPrefix() string {
	dir, err := s.profilesDir()
	if err != nil {
		// An unresolvable profile directory means nothing is editable, which
		// is the safe answer: the page then offers no button that would fail.
		return "\x00 no profile directory"
	}
	return dir + string(os.PathSeparator)
}

// handleProfileDocument is the export: the canonical bytes of one document.
//
// Canonical, not the file as it happens to be written, so what is exported is
// what the digest covers — an exported profile that round-trips to a different
// digest would be an export nobody could verify against the copy it came from.
func (s *Server) handleProfileDocument(w http.ResponseWriter, r *http.Request) {
	entry, _, err := s.profileEntry(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	document, err := profile.Export(entry.Profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": entry.Profile.Metadata().ID, "digest": entry.Digest,
		"trust": entry.Trust, "source": entry.Source,
		"document": json.RawMessage(document),
	})
}

// handleProfileTemplates lists the tested documents the wizard can start from.
//
// The built-ins, and only the built-ins. Not because a community profile is a
// bad starting point, but because "tested" is a claim, and the only documents
// this program can make it about are the ones that shipped with it — with the
// qualification each of them carries: see internal/profile/builtin, where the
// engines are qualified by documentation and say so.
func (s *Server) handleProfileTemplates(w http.ResponseWriter, r *http.Request) {
	entries, err := builtin.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	kind := r.URL.Query().Get("kind")
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		meta := entry.Profile.Metadata()
		if kind != "" && string(meta.Kind) != kind {
			continue
		}
		item := map[string]any{
			"id": meta.ID, "kind": meta.Kind, "name": meta.Name,
			"summary": meta.Summary, "version": meta.Version,
			"digest": entry.Digest, "file": entry.File,
			"capabilities": capabilitiesOf(entry.Profile),
			"actions":      actionIDs(entry.Profile),
			"permissions":  profile.PermissionIDs(entry.Profile),
		}
		if document, isEngine := entry.Profile.(*profile.EngineProfile); isEngine {
			item["runtime"] = document.Runtime
			item["engine_version"] = document.EngineVersion
			item["executables"] = document.Executables
			item["platforms"] = document.Platforms
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func capabilitiesOf(p profile.Profile) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, action := range p.ActionList() {
		if action.Capability == "" || seen[action.Capability] {
			continue
		}
		seen[action.Capability] = true
		out = append(out, action.Capability)
	}
	sort.Strings(out)
	return out
}

func actionIDs(p profile.Profile) []string {
	out := make([]string, 0, len(p.ActionList()))
	for _, action := range p.ActionList() {
		out = append(out, action.ID)
	}
	return out
}

// composeRequest is what the wizard's forms send.
//
// Every member is optional and an omitted one leaves the template's value
// alone. That is what makes the wizard usable in any order: a user who has only
// decided on a name can compose, look at the result, and come back.
type composeRequest struct {
	// Template is the built-in id to start from. Required: this route edits a
	// document, it does not invent one.
	Template string `json:"template"`
	// Document, when present, is edited instead of the template. It is how the
	// advanced JSON view and the forms stay the same document: the page sends
	// back what it is showing, and the forms apply on top of it.
	Document json.RawMessage `json:"document,omitempty"`

	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Version     string `json:"version,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`

	PublisherName string `json:"publisher_name,omitempty"`
	PublisherURL  string `json:"publisher_url,omitempty"`
	LicenseSPDX   string `json:"license_spdx,omitempty"`
	LicenseName   string `json:"license_name,omitempty"`

	Runtime       string `json:"runtime,omitempty"`
	EngineVersion string `json:"engine_version,omitempty"`
	LastQualified string `json:"last_qualified,omitempty"`

	// Executables renames the file each declared executable looks for. Keyed by
	// the declared name, so a profile with a client and a dedicated-server
	// binary can set both.
	Executables map[string]string `json:"executables,omitempty"`
	// Actions, when present, is the subset of the template's actions to keep.
	// An engine that cannot host a dedicated server says so by not declaring
	// the action — see internal/profile — so removing one is how the wizard
	// expresses that, and there is deliberately no way to add one the template
	// did not have.
	Actions []string `json:"actions,omitempty"`

	// Scratch, when present and no Document is being edited, is a profile
	// written from nothing — see scratch.go. The identity members above still
	// apply on top of it.
	Scratch *scratchDocument `json:"scratch,omitempty"`
}

// handleProfileCompose applies the wizard's fields and returns the result.
//
// It writes nothing. What comes back is a document, its validation, its digest,
// its permissions and a normalized diff against what it started from — which is
// everything somebody needs in order to decide whether to import it, and none
// of it requires having imported it first.
func (s *Server) handleProfileCompose(w http.ResponseWriter, r *http.Request) {
	var request composeRequest
	if !decodeJSON(w, r, &request) {
		return
	}

	base, from, err := s.composeBase(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	edited, err := applyComposeFields(base, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	encoded, err := json.Marshal(edited)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	body := map[string]any{"from": from, "document": json.RawMessage(encoded)}
	document, decodeErr := profile.Decode(encoded)
	if decodeErr != nil {
		// 200 with `valid: false`. The wizard is mid-edit: a document that does
		// not validate yet is the normal state of a form somebody is filling
		// in, and every message the validator produced is what the page shows
		// beside the field it belongs to.
		body["valid"] = false
		body["error"] = decodeErr.Error()
		writeJSON(w, http.StatusOK, body)
		return
	}
	digest, err := profile.Digest(document)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Canonical bytes, so what the advanced view shows is what the digest
	// covers — and so the JSON a user exports is the JSON they can import
	// somewhere else and get the same digest for.
	canonical, err := profile.Export(document)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	meta := document.Metadata()
	body["valid"] = true
	body["document"] = json.RawMessage(canonical)
	body["id"] = meta.ID
	body["kind"] = meta.Kind
	body["name"] = meta.Name
	body["version"] = meta.Version
	body["digest"] = digest
	body["permissions"] = document.Permissions()
	body["report"] = profile.PermissionReport(document, profile.TrustLocal)
	body["trust"] = profile.TrustLocal
	body["actions"] = actionIDs(document)

	if source, err := s.templateProfile(from); err == nil {
		if difference, err := profile.DiffProfiles(source, document); err == nil {
			body["diff"] = describeDiff(difference)
		}
	}
	writeJSON(w, http.StatusOK, body)
}

// composeBase is the document the wizard's fields are applied to: the one the
// page sent back, or the template it named.
func (s *Server) composeBase(request composeRequest) (map[string]any, string, error) {
	if len(request.Document) > 0 {
		var tree map[string]any
		if err := json.Unmarshal(request.Document, &tree); err != nil {
			return nil, "", fmt.Errorf("the document being edited is not a JSON object: %w", err)
		}
		return tree, request.Template, nil
	}
	if request.Scratch != nil {
		tree, err := scratchTree(*request.Scratch)
		if err != nil {
			return nil, "", err
		}
		return tree, "", nil
	}
	if request.Template == "" {
		return nil, "", errors.New(
			"a profile starts from a tested template, or is written from scratch: name one from /api/v1/profiles/templates, or send `scratch`")
	}
	entry, err := builtin.Find(request.Template)
	if err != nil {
		return nil, "", err
	}
	encoded, err := profile.Export(entry.Profile)
	if err != nil {
		return nil, "", err
	}
	var tree map[string]any
	if err := json.Unmarshal(encoded, &tree); err != nil {
		return nil, "", err
	}
	return tree, request.Template, nil
}

func (s *Server) templateProfile(id string) (profile.Profile, error) {
	entry, err := builtin.Find(id)
	if err != nil {
		return nil, err
	}
	return entry.Profile, nil
}

// applyComposeFields edits a decoded document tree.
//
// It works on the generic tree rather than on a typed struct, because the
// wizard must be able to start from a document this build does not have a
// Go type for — a pipeline, or a kind added later. Every member it touches is
// one the schema defines; anything it does not understand it leaves exactly
// where it was, which is what stops an edit from silently dropping a member.
func applyComposeFields(tree map[string]any, request composeRequest) (map[string]any, error) {
	setString := func(key, value string) {
		if value != "" {
			tree[key] = value
		}
	}
	setString("id", strings.TrimSpace(request.ID))
	setString("name", strings.TrimSpace(request.Name))
	setString("version", strings.TrimSpace(request.Version))
	setString("summary", strings.TrimSpace(request.Summary))
	setString("description", strings.TrimSpace(request.Description))
	setString("runtime", strings.TrimSpace(request.Runtime))
	setString("engine_version", strings.TrimSpace(request.EngineVersion))
	setString("last_qualified", strings.TrimSpace(request.LastQualified))

	if request.PublisherName != "" || request.PublisherURL != "" {
		publisher, _ := tree["publisher"].(map[string]any)
		if publisher == nil {
			publisher = map[string]any{}
		}
		if request.PublisherName != "" {
			publisher["name"] = request.PublisherName
		}
		if request.PublisherURL != "" {
			publisher["url"] = request.PublisherURL
		} else if request.PublisherName != "" {
			// A new publisher with the template's URL would credit somebody
			// else's homepage for this user's document.
			delete(publisher, "url")
		}
		tree["publisher"] = publisher
	}
	if request.LicenseSPDX != "" || request.LicenseName != "" {
		license, _ := tree["license"].(map[string]any)
		if license == nil {
			license = map[string]any{}
		}
		if request.LicenseSPDX != "" {
			license["spdx"] = request.LicenseSPDX
		}
		if request.LicenseName != "" {
			license["name"] = request.LicenseName
		}
		tree["license"] = license
	}

	if len(request.Executables) > 0 {
		list, _ := tree["executables"].([]any)
		declared := map[string]bool{}
		for _, raw := range list {
			executable, _ := raw.(map[string]any)
			if executable == nil {
				continue
			}
			name, _ := executable["name"].(string)
			declared[name] = true
			if file, present := request.Executables[name]; present && strings.TrimSpace(file) != "" {
				executable["file"] = strings.TrimSpace(file)
			}
		}
		for name := range request.Executables {
			if !declared[name] {
				return nil, fmt.Errorf("this template declares no executable called %q", name)
			}
		}
	}

	if request.Actions != nil {
		wanted := map[string]bool{}
		for _, id := range request.Actions {
			wanted[id] = true
		}
		list, _ := tree["actions"].([]any)
		kept := make([]any, 0, len(list))
		offered := make([]string, 0, len(list))
		for _, raw := range list {
			action, _ := raw.(map[string]any)
			if action == nil {
				continue
			}
			id, _ := action["id"].(string)
			offered = append(offered, id)
			if wanted[id] {
				kept = append(kept, raw)
				delete(wanted, id)
			}
		}
		if len(wanted) > 0 {
			missing := make([]string, 0, len(wanted))
			for id := range wanted {
				missing = append(missing, id)
			}
			sort.Strings(missing)
			return nil, fmt.Errorf(
				"this template has no %s action; it offers %s. "+
					"An action describes something the program actually does, so one has to be "+
					"written rather than named",
				strings.Join(missing, ", "), strings.Join(offered, ", "))
		}
		tree["actions"] = kept
	}
	return tree, nil
}

func describeDiff(difference profile.Diff) map[string]any {
	changes := make([]map[string]any, 0, len(difference.Changes))
	for _, change := range difference.Changes {
		changes = append(changes, map[string]any{
			"path": change.Path, "kind": change.Kind,
			"before": change.Before, "after": change.After,
		})
	}
	return map[string]any{
		"empty":     difference.Empty(),
		"escalates": difference.Escalates(),
		"changes":   changes,
		"text":      difference.String(),
	}
}

// handleProfileDiff compares a document against one this machine already has.
//
// It is the review step for an update: what a new version changed, and whether
// it asks for more than the one that was approved. `escalates` is the answer
// that matters, and it is [profile.Diff]'s own.
func (s *Server) handleProfileDiff(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Against  string          `json:"against"`
		Document json.RawMessage `json:"document"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if len(request.Document) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no profile document was sent"))
		return
	}
	incoming, err := profile.Decode(request.Document)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"valid": false, "error": err.Error(),
		})
		return
	}
	against := request.Against
	if against == "" {
		against = incoming.Metadata().ID
	}
	entry, local, err := s.profileEntry(against)
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	difference, err := profile.DiffProfiles(entry.Profile, incoming)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	body := map[string]any{
		"valid": true, "against": against, "installed_digest": entry.Digest,
		"diff": describeDiff(difference),
	}
	// The immutability rule: the same version saying something different is a
	// republish, and it is refused rather than diffed away.
	if err := profile.CheckVersionImmutable(entry.Profile, incoming); err != nil {
		body["republished"] = err.Error()
	}
	if local.Grant != nil && difference.Escalates() {
		body["grant_invalidated"] = true
	}
	writeJSON(w, http.StatusOK, body)
}

// handleProfileImport writes a document into the profile directory.
//
// It grants nothing. An imported profile is `local` — nobody vouched for it,
// including the person who just pasted it — and it cannot run until somebody
// reads what it asks for and approves it against its digest. That separation is
// the whole trust model, and an import route that also granted would delete it.
func (s *Server) handleProfileImport(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Document json.RawMessage `json:"document"`
		// Replace allows overwriting a document already in the profile
		// directory. Off by default: an import that silently replaced an
		// approved profile would be an import that changed what is allowed to
		// run without anybody reading it.
		Replace bool `json:"replace,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if len(request.Document) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no profile document was sent"))
		return
	}
	document, err := profile.Decode(request.Document)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"valid": false, "error": err.Error(),
		})
		return
	}
	meta := document.Metadata()
	digest, err := profile.Digest(document)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}

	dir, err := s.profilesDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	name, err := profileFileName(meta)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	path := filepath.Join(dir, name)

	catalog, err := s.catalog()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if existing, lookupErr := catalog.Lookup(meta.ID); lookupErr == nil {
		if !strings.HasPrefix(existing.Source, s.profilesPrefix()) {
			writeError(w, http.StatusConflict, fmt.Errorf(
				"%s is already provided by %s, and a document that shipped with the "+
					"Companion is not something an import may replace", meta.ID, existing.Source))
			return
		}
		if !request.Replace {
			writeError(w, http.StatusConflict, fmt.Errorf(
				"%s is already installed here (%s); "+
					"look at what changed, then import it again to replace it",
				meta.ID, existing.Source))
			return
		}
		if err := profile.CheckVersionImmutable(existing.Profile, document); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		path = existing.Source
	}

	canonical, err := profile.Export(document)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Written atomically and canonically: what lands is what the digest above
	// covers, and a crash mid-write leaves the previous document rather than
	// half of a new one.
	temporary := path + ".writing"
	if err := os.WriteFile(temporary, append(canonical, '\n'), 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.Rename(temporary, path); err != nil {
		os.Remove(temporary)
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	entry, local, err := s.profileEntry(meta.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	body := s.describeCatalogEntry(entry, local)
	body["imported_to"] = path
	body["digest"] = digest
	writeJSON(w, http.StatusCreated, body)
}

// profileFileName is where a document lands. The id, with the characters that
// are not a filename replaced — a value from a request body becomes a path
// here, so it is restricted rather than cleaned.
func profileFileName(meta profile.Meta) (string, error) {
	if meta.ID == "" {
		return "", errors.New("this document has no id")
	}
	var builder strings.Builder
	for _, r := range meta.ID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	name := strings.Trim(builder.String(), "-._")
	if name == "" || len(name) > 120 {
		return "", fmt.Errorf("%q cannot be a file name on this machine", meta.ID)
	}
	return name + ".json", nil
}

// handleProfileGrant records that a user approved what a profile asks for.
//
// The digest is required and must be the document on disk. A grant is an
// approval of one exact document — see [profile.NewGrant] — and accepting one
// for a digest the page last saw would be accepting an approval of something
// that has since changed.
//
// Nothing about that decision is implemented here. [approval.Service] is the one
// writer of a grant on this machine and `companion profile grant` holds the same
// one, so the page and the command line cannot come to disagree about what an
// approval is or how it is stored. See internal/approval.
func (s *Server) handleProfileGrant(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Digest string `json:"digest"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	service, err := s.approvals()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	decision, err := service.Grant(r.PathValue("id"), request.Digest)
	if err != nil {
		writeError(w, approvalStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.describeCatalogEntry(decision.Entry, decision.Binding))
}

// handleProfileWithdraw takes an approval back. The paths stay: where a program
// is on this machine is not part of what was approved.
func (s *Server) handleProfileWithdraw(w http.ResponseWriter, r *http.Request) {
	service, err := s.approvals()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	decision, err := service.Withdraw(r.PathValue("id"))
	if err != nil {
		writeError(w, approvalStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.describeCatalogEntry(decision.Entry, decision.Binding))
}

// approvalStatus maps the approval service's refusals onto the statuses the page
// already distinguishes: a missing digest is a malformed request, a stale one is
// a conflict with the document on disk, and everything else falls through to the
// job mapping so an unknown profile is still a 404.
func approvalStatus(err error) int {
	var stale *approval.StaleDigestError
	switch {
	case errors.Is(err, approval.ErrDigestRequired):
		return http.StatusBadRequest
	case errors.As(err, &stale):
		return http.StatusConflict
	}
	return jobStatus(err)
}

// handleProfileRemove deletes an imported document.
//
// Only one under the profile directory: a built-in document is inside the
// binary. The grant goes with it, because a grant for a document that is not
// there is an approval nothing can check.
func (s *Server) handleProfileRemove(w http.ResponseWriter, r *http.Request) {
	entry, _, err := s.profileEntry(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	if !strings.HasPrefix(entry.Source, s.profilesPrefix()) {
		writeError(w, http.StatusConflict, fmt.Errorf(
			"%s comes from %s, which is not a file this can remove", entry.Profile.Metadata().ID, entry.Source))
		return
	}
	if err := os.Remove(entry.Source); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	path, err := s.bindingsPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := binding.Update(path, func(set *binding.Set) error {
		set.Remove(entry.Profile.Metadata().ID)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"removed": entry.Profile.Metadata().ID, "was": entry.Source,
	})
}

// handleProfileValidate checks a document a caller pasted in, without importing
// it and without running anything.
//
// The whole point of the trust model is that reading a stranger's profile is
// inert, so this is the one route that takes a document body: it decodes,
// validates, digests and summarises the permissions, which is exactly what
// somebody needs in front of them before they decide to grant anything.
func (s *Server) handleProfileValidate(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("reading the profile document: %w", err))
		return
	}
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no profile document was sent"))
		return
	}
	document, decodeErr := profile.Decode(raw)
	if decodeErr != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"valid": false,
			"error": decodeErr.Error(),
		})
		return
	}
	digest, err := profile.Digest(document)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	meta := document.Metadata()
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":       true,
		"id":          meta.ID,
		"version":     meta.Version,
		"kind":        meta.Kind,
		"name":        meta.Name,
		"summary":     meta.Summary,
		"digest":      digest,
		"permissions": document.Permissions(),
		// `local` because that is what a document somebody pasted is. Saying so
		// here means the caller is told what it would take to run it, rather
		// than finding out when it is refused.
		"trust":  profile.TrustLocal,
		"report": profile.PermissionReport(document, profile.TrustLocal),
	})
}

// describeProfile is one catalog entry's portable half, and only that half.
func describeProfile(entry job.CatalogEntry) map[string]any {
	meta := entry.Profile.Metadata()
	actions := make([]map[string]any, 0, len(entry.Profile.ActionList()))
	for _, action := range entry.Profile.ActionList() {
		actions = append(actions, map[string]any{
			"id":              action.ID,
			"title":           action.Title,
			"description":     action.Description,
			"capability":      action.Capability,
			"session_role":    action.SessionRole,
			"inputs":          action.Inputs,
			"outputs":         action.Outputs,
			"options":         action.Options,
			"timeout_seconds": action.TimeoutSeconds,
			// The roots an action needs, because they are half of what a setup
			// form has to ask for and half of what a permission review has to
			// show. An action reaches nothing that is not listed here.
			"roots":   action.Roots,
			"network": action.Network,
		})
	}
	family := documentFamily(entry.Profile)
	return map[string]any{
		"id":          meta.ID,
		"kind":        meta.Kind,
		"version":     meta.Version,
		"name":        meta.Name,
		"summary":     meta.Summary,
		"description": meta.Description,
		"publisher":   meta.Publisher,
		"license":     meta.License,
		"trust":       entry.Trust,
		"digest":      entry.Digest,
		"source":      entry.Source,
		"permissions": entry.Profile.Permissions(),
		"actions":     actions,
		// Every description of a profile carries the family it is for and what
		// this build says about that family. On every route, not only the ones
		// that happen to draw a badge today: a surface added next year gets the
		// statement without anybody remembering to plumb it through.
		"engine_family": family,
		"maturity":      describeMaturity(family),
	}
}

// bindRequest is what a setup form sends: where this machine keeps the programs
// a profile declares, and which directories it may reach.
//
// Explicit per-executable and per-root maps rather than one "program" and one
// "game root": a profile may declare two executables (a client and a
// dedicated-server binary) or a root this build has never heard of, and a form
// shaped around the two common ones would be a form that cannot set up the
// third.
//
// It is not engine-specific, and that matters beyond tidiness. A hand-installed
// compiler needs exactly the same thing said about it as a hand-installed
// engine — where it is — and a bind route that only worked for engines would
// leave the Build area with a tool it can see, can describe, has been approved,
// and cannot start.
type bindRequest struct {
	Executables map[string]string `json:"executables,omitempty"`
	// Folder is a directory the user chose that holds the profile's programs
	// the way the profile lays them out — an unpacked ericw-tools release, say.
	// Every declared executable is found under it, or the request is refused
	// naming the ones that are not there; nothing is recorded from a folder
	// that holds half a toolchain (NEW_244D).
	Folder string `json:"folder,omitempty"`
	Roots       map[string]string `json:"roots,omitempty"`
	// Approve records that the user read what the profile asks for and agreed
	// to it. It is against one exact digest — see [profile.NewGrant].
	Approve bool `json:"approve,omitempty"`
	// Digest is the document the user was looking at when they approved. The
	// request is refused if it is not the one on disk, because an approval for
	// a document that has changed since it was displayed is an approval for
	// something nobody read.
	Digest string `json:"digest,omitempty"`
}

func (s *Server) handleProfileBind(w http.ResponseWriter, r *http.Request) {
	var request bindRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	entry, _, err := s.profileEntry(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	document := entry.Profile
	meta := document.Metadata()

	if len(request.Executables) == 0 && len(request.Roots) == 0 && request.Folder == "" && !request.Approve {
		writeError(w, http.StatusBadRequest,
			errors.New("nothing to record: send an executable, a folder, a root, or an approval"))
		return
	}
	if request.Folder != "" {
		if len(request.Executables) > 0 {
			writeError(w, http.StatusBadRequest,
				errors.New("send a folder or individual programs, not both"))
			return
		}
		found, err := executablesInFolder(document, request.Folder)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		request.Executables = found
	}

	// What the request asks for, checked and resolved before anything is
	// written. An empty value is a removal, which is why the maps hold a
	// pointer-free "" rather than being absent.
	declared := declaredExecutables(document)
	executables := map[string]string{}
	for name, value := range request.Executables {
		if !declared[name] {
			writeError(w, http.StatusBadRequest, fmt.Errorf(
				"%s declares no executable called %q", meta.ID, name))
			return
		}
		if value == "" {
			executables[name] = ""
			continue
		}
		resolved, err := checkExecutable(value)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("the %s program: %w", name, err))
			return
		}
		executables[name] = resolved
	}
	roots := map[string]string{}
	for role, value := range request.Roots {
		if role == profile.RootWorkspace {
			writeError(w, http.StatusBadRequest, fmt.Errorf(
				"the %q root is created for each job and is not something to record",
				profile.RootWorkspace))
			return
		}
		if value == "" {
			roots[role] = ""
			continue
		}
		resolved, err := checkDirectory(value)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("the %s root: %w", role, err))
			return
		}
		roots[role] = resolved
	}

	path, err := s.bindingsPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// The read, the change and the write inside one cross-process lock. The GUI
	// server is one process and a `companion` command in a terminal is another;
	// without this, whichever renamed last would silently discard the other's
	// change. See [binding.Update].
	var local binding.LocalBinding
	updated, err := binding.Update(path, func(set *binding.Set) error {
		local, _ = set.Find(meta.ID)
		if local.ProfileDigest != entry.Digest {
			// The approval was for one exact document, so a changed document
			// invalidates it. The paths survive: where a program and its data are
			// is a fact about this machine, not about the document, and dropping
			// them would mean an upgrade quietly forgetting what the user set.
			local.Grant = nil
		}
		local.ProfileID = meta.ID
		local.ProfileVersion = meta.Version
		local.ProfileDigest = entry.Digest
		local.Trust = entry.Trust
		if local.Acquisition == "" {
			local.Acquisition = profile.AcquireUserPath
		}
		// A program the user named is a program the user named, whatever put
		// the previous paths here. Leaving `managed_download` on a binding
		// whose paths a person has just replaced would describe bytes nothing
		// verified as verified (NEW_244D).
		if len(executables) > 0 && local.Acquisition != profile.AcquireUserPath {
			local.Acquisition = profile.AcquireUserPath
			local.Installs = nil
		}
		for name, value := range executables {
			if value == "" {
				delete(local.Executables, name)
				continue
			}
			if local.Executables == nil {
				local.Executables = map[string]string{}
			}
			local.Executables[name] = value
		}
		for role, value := range roots {
			if value == "" {
				delete(local.Roots, role)
				continue
			}
			if local.Roots == nil {
				local.Roots = map[string]string{}
			}
			local.Roots[role] = value
		}
		local.UpdatedAt = time.Now().UTC()
		return set.Put(local)
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if stored, found := updated.Find(meta.ID); found {
		local = stored
	}

	// The approval is a second, separate decision, and it is recorded by the one
	// service that records approvals — the same one `companion profile grant`
	// holds. Binding does not imply it: a request with no `approve` writes the
	// paths and grants nothing, which is what makes "set this up now, decide
	// later" a state the program actually has.
	if request.Approve {
		service, err := s.approvals()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		decision, err := service.Grant(meta.ID, request.Digest)
		if err != nil {
			writeError(w, approvalStatus(err), err)
			return
		}
		local = decision.Binding
	}
	writeJSON(w, http.StatusOK, s.describeCatalogEntry(entry, local))
}

// handleProfileUnbind forgets this machine's setup for one profile.
//
// It removes a record and touches nothing the record pointed at: the program,
// the game data and anything staged into it are the user's. A "remove" that
// deleted files would be a remove nobody could safely press.
func (s *Server) handleProfileUnbind(w http.ResponseWriter, r *http.Request) {
	entry, _, err := s.profileEntry(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	path, err := s.bindingsPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	removed := false
	if _, err := binding.Update(path, func(set *binding.Set) error {
		removed = set.Remove(entry.Profile.Metadata().ID)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"removed": removed,
		"profile": s.describeCatalogEntry(entry, binding.LocalBinding{}),
	})
}

// describeExecutables is the declared programs, for a setup form.
func describeExecutables(document profile.Profile) []map[string]any {
	out := make([]map[string]any, 0)
	add := func(name, title, file string) {
		out = append(out, map[string]any{"name": name, "title": title, "file": file})
	}
	switch typed := document.(type) {
	case *profile.EngineProfile:
		for _, executable := range typed.Executables {
			add(executable.Name, executable.Title, executable.File)
		}
	case *profile.ToolProfile:
		for _, executable := range typed.Executables {
			add(executable.Name, executable.Title, executable.File)
		}
	}
	return out
}

// declaredExecutables is the names a document says it starts. Read off the
// typed documents rather than the JSON, so a kind that declares none — a
// pipeline — reports none rather than reporting nothing at all.
func declaredExecutables(document profile.Profile) map[string]bool {
	names := map[string]bool{}
	switch typed := document.(type) {
	case *profile.EngineProfile:
		for _, executable := range typed.Executables {
			names[executable.Name] = true
		}
	case *profile.ToolProfile:
		for _, executable := range typed.Executables {
			names[executable.Name] = true
		}
	}
	return names
}

// checkExecutable is the file half of what a binding records: an existing file,
// absolute, that is not a directory.
//
// It does not check the executable bit. A file whose permissions are wrong is a
// file the user can fix and still the file they meant, and refusing it here
// would mean the setup form rejecting a correct answer — the executor's own
// failure names the real problem far better.
func checkExecutable(path string) (string, error) {
	resolved, err := checkOpenFile(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a program on this machine", resolved)
	}
	return resolved, nil
}

// executablesInFolder finds every executable a profile declares under a folder
// the user chose, laid out the way the profile says (`bin/qbsp`, …).
//
// A person who opens the `bin` directory itself rather than the folder above
// it has still pointed at the right programs, so a folder whose name is the
// first element every declared path shares is also tried from its parent.
// Every file must be there: a partial toolchain is refused naming what is
// missing, because a build that fails on its third stage for a program this
// setup quietly left out is worse than a setup that says so now.
func executablesInFolder(document profile.Profile, folder string) (map[string]string, error) {
	root, err := checkDirectory(folder)
	if err != nil {
		return nil, fmt.Errorf("the folder: %w", err)
	}
	var declared []profile.Executable
	switch typed := document.(type) {
	case *profile.EngineProfile:
		declared = typed.Executables
	case *profile.ToolProfile:
		declared = typed.Executables
	}
	if len(declared) == 0 {
		return nil, errors.New("this profile declares no programs to find in a folder")
	}
	suffix := currentPlatform().ExeSuffix()
	find := func(base string) (map[string]string, []string) {
		found := map[string]string{}
		var missing []string
		for _, executable := range declared {
			relative := strings.ReplaceAll(executable.File, "{platform.exe_suffix}", suffix)
			path := filepath.Join(base, filepath.FromSlash(relative))
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				missing = append(missing, relative)
				continue
			}
			found[executable.Name] = path
		}
		return found, missing
	}
	found, missing := find(root)
	if len(missing) > 0 {
		first := strings.SplitN(strings.ReplaceAll(declared[0].File, "{platform.exe_suffix}", suffix), "/", 2)
		if len(first) == 2 && filepath.Base(root) == first[0] {
			if alternative, stillMissing := find(filepath.Dir(root)); len(stillMissing) == 0 {
				return alternative, nil
			}
		}
		return nil, fmt.Errorf("%s does not hold this profile's programs: missing %s",
			root, strings.Join(missing, ", "))
	}
	return found, nil
}
