package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/publish"
)

// The account in the header, and "Sync profiles" (NEW_244D, HITL; named for
// what it syncs by NEW_508 — it was "Sync to cloud").
//
// # What the operator decided
//
// The Companion is meant to be used signed in: the maps it compiles are made in
// the editor, which needs an account, and the games it starts are published in
// the gallery, which needs one too. Being offline is a fallback, and coming back
// must make syncing obvious. A person's profiles follow their account (they are
// not separate per account on one machine).
//
// # What "in sync" means, and what never leaves the machine
//
// A profile the person wrote or installed is synced as a PRIVATE publication in
// their own account — AUB's existing profile catalogue, with its existing
// visibility — through publish.PreviewOf and publish.Publish, the ONE export gate
// (AGENTS.md §2): a document holding a path or a credential cannot even be
// previewed, let alone synced. Where a program is on this machine, and whether
// this machine approved a document, are bindings and grants; they are facts
// about a computer and are never sent.
//
// Going the other way a profile in the account is installed through
// publish.PlanInstall and publish.Apply, so it arrives exactly as any listing
// does: re-digested and re-canonicalised here, recorded as `community` whatever
// the deployment says (§2.3), and only because the person pressed Sync after
// reading the list of what it would install and what each asks to be allowed to
// do. Built-in documents ship with the program and are never synced.
//
// # States
//
//	synced         the account holds this version, byte for byte
//	to_upload      only here, or here at a version the account does not hold
//	to_download    only in the account, or the account holds a newer version
//	conflict       the same id AND version with different bytes: a version is a
//	               promise about bytes, so nothing is overwritten either way and
//	               the person is told to raise the version of the one they keep

type accountProfile struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	State     string `json:"state"`
	Local     string `json:"local_version,omitempty"`
	Remote    string `json:"account_version,omitempty"`
	ListingID string `json:"listing_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
	// Permissions are what a document to download asks for, shown in the
	// review before anything is installed.
	Permissions []string `json:"permissions,omitempty"`
}

type accountStatus struct {
	SignedIn    bool   `json:"signed_in"`
	Email       string `json:"email,omitempty"`
	ServerLabel string `json:"server_label,omitempty"`
	Online      bool   `json:"online"`
	OfflineWhy  string `json:"offline_reason,omitempty"`
	// Blocked is AUB's reason code when the account answers but refuses until
	// the person does something there (`legal_acceptance_required`).
	Blocked      string           `json:"blocked,omitempty"`
	CheckedAt    time.Time        `json:"checked_at"`
	InSync       bool             `json:"in_sync"`
	ToUpload     int              `json:"to_upload"`
	ToDownload   int              `json:"to_download"`
	Conflicts    int              `json:"conflicts"`
	Profiles     []accountProfile `json:"profiles"`
	SyncedAspect string           `json:"synced_aspect"`
}

func (s *Server) accountAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/account":       s.handleAccountStatus,
		"POST /api/v1/account/sync": s.handleAccountSync,
	}
}

// serverLabel names the server in words a person uses: an official deployment's
// name, or "a development server" — never an address with a port (operator).
func serverLabel(address string) string {
	if label := backendLabel(address); label != "" {
		return label
	}
	if address == "" {
		return ""
	}
	return "a development server"
}

func (s *Server) handleAccountStatus(w http.ResponseWriter, r *http.Request) {
	status, _, _ := s.accountState(r.Context())
	writeJSON(w, http.StatusOK, status)
}

// accountState compares this machine's profiles with the account's own.
func (s *Server) accountState(ctx context.Context) (accountStatus, map[string]profile.Profile, map[string]aub.PublishedProfile) {
	status := accountStatus{CheckedAt: time.Now().UTC(), SyncedAspect: "profiles", Profiles: []accountProfile{}}
	client := s.aubClient()
	if client == nil || !client.Authenticated() {
		return status, nil, nil
	}
	status.SignedIn = true
	status.Email = s.config().Session.Email
	status.ServerLabel = serverLabel(client.BaseURL())

	// Written here (`local`) and installed from a listing (`community`) are
	// both on this machine; only the first is the person's own to upload. A
	// community document is somebody's publication — possibly the person's own
	// account's, arriving on a second machine — and is compared, never re-sent.
	local := map[string]profile.Profile{}
	authored := map[string]bool{}
	if catalog, err := s.catalog(); err == nil {
		if entries, err := catalog.List(); err == nil {
			for _, entry := range entries {
				if entry.Trust == profile.TrustBuiltin {
					continue
				}
				id := entry.Profile.Metadata().ID
				local[id] = entry.Profile
				authored[id] = entry.Trust == profile.TrustLocal
			}
		}
	}

	checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	mine, err := client.ProfileCatalogAll(checkCtx, aub.ProfileQuery{Mine: true}, 500)
	if err != nil {
		// Offline, or the session lapsed: say which, and describe only what is
		// here. Nothing is guessed about the account.
		status.OfflineWhy = offlineReason(err)
		status.Blocked = blockedBy(err)
		for id, document := range local {
			meta := document.Metadata()
			status.Profiles = append(status.Profiles, accountProfile{
				ID: id, Kind: string(meta.Kind), Name: meta.Name, State: "unknown", Local: meta.Version,
			})
		}
		sortProfiles(status.Profiles)
		return status, local, nil
	}
	status.Online = true

	remote := map[string]aub.PublishedProfile{}
	for _, listing := range mine {
		remote[listing.ProfileID] = listing
	}
	ids := map[string]bool{}
	for id := range local {
		ids[id] = true
	}
	for id := range remote {
		ids[id] = true
	}
	for id := range ids {
		document, here := local[id]
		listing, there := remote[id]
		row := accountProfile{ID: id}
		if here {
			meta := document.Metadata()
			row.Kind, row.Name, row.Local = string(meta.Kind), meta.Name, meta.Version
		}
		if there {
			row.ListingID, row.Remote = listing.ID, listing.LatestVersion
			if row.Name == "" {
				row.Kind, row.Name = listing.Kind, listing.Name
			}
		}
		switch {
		case here && !there && !authored[id]:
			// Installed from somebody else's listing: not this account's to hold.
			continue
		case here && !there:
			row.State = "to_upload"
		case !here && there:
			row.State = "to_download"
		default:
			row.State = compareVersions(ctx, client, document, listing)
			if row.State == "to_upload" && !authored[id] {
				row.State = "synced"
			}
		}
		switch row.State {
		case "to_upload":
			status.ToUpload++
		case "to_download":
			status.ToDownload++
		case "conflict":
			status.Conflicts++
			row.Reason = "this machine and the account hold different documents under the same version; raise the version of the one to keep"
		}
		status.Profiles = append(status.Profiles, row)
	}
	sortProfiles(status.Profiles)
	status.InSync = status.ToUpload == 0 && status.ToDownload == 0 && status.Conflicts == 0
	return status, local, remote
}

// compareVersions decides a profile present on both sides.
func compareVersions(ctx context.Context, client *aub.Client, document profile.Profile, listing aub.PublishedProfile) string {
	meta := document.Metadata()
	digest, err := profile.Digest(document)
	if err != nil {
		return "conflict"
	}
	detail, err := client.PublishedProfileByID(ctx, listing.ID)
	if err != nil {
		return "unknown"
	}
	for _, version := range detail.Versions {
		if version.Version != meta.Version {
			continue
		}
		if version.Digest == digest {
			if listing.LatestVersion != "" && listing.LatestVersion != meta.Version {
				return "to_download"
			}
			return "synced"
		}
		return "conflict"
	}
	// The account does not hold this version. Which side is newer decides the
	// direction; a local version the account never saw is an upload.
	mineV, errMine := profile.ParseVersion(meta.Version)
	theirs, errTheirs := profile.ParseVersion(listing.LatestVersion)
	if errMine != nil || errTheirs != nil || mineV.Compare(theirs) > 0 {
		return "to_upload"
	}
	return "to_download"
}

type syncRequest struct {
	// DryRun answers what a sync WOULD do — each upload's disclosures and each
	// download's permissions — and does nothing. The page shows it as the review.
	DryRun bool `json:"dry_run"`
	// Confirmed is the person pressing Sync profiles after reading the review.
	// Both halves need it: publishing needs a confirmation and installing an
	// approval (AGENTS.md §2.2), and neither is defaulted.
	Confirmed bool `json:"confirmed"`
	// Reviewed is the identity of the review the person read: the dry run's
	// `reviewed`. A confirmation is for THAT list. Without it, or with one that
	// is not the list as it stands now, nothing is sent, installed or approved
	// and the answer is 409 with the current review (NEW_508A).
	Reviewed string `json:"reviewed,omitempty"`
}

type syncOutcome struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Action      string   `json:"action"`
	Result      string   `json:"result"`
	Error       string   `json:"error,omitempty"`
	Disclosures int      `json:"disclosures,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
	// Version and Digest are the exact document the operation is about: the
	// bytes that would be published, or the bytes that were served and checked.
	Version string `json:"version,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// syncOperation is one thing a sync would do, PLANNED: the outcome the review
// shows, and the very preview or install plan that will be executed. The plan
// is made once. The review's identity is computed from it, and the press
// executes it — nothing is resolved a second time between the two, so "the
// latest version" cannot become a different version after it was reviewed
// (NEW_508A: the fingerprint used to hash summary rows made BEFORE the plans,
// and the install was planned with an empty version).
type syncOperation struct {
	outcome syncOutcome
	upload  *publish.Preview
	install *publish.Plan
	// identity is what the confirmation is bound to, beyond the outcome's own
	// fields: every disclosure and every permission, in full.
	identity []string
}

// planSync turns the account's state into the exact operations a sync would
// perform. It writes nothing anywhere.
func (s *Server) planSync(ctx context.Context, status accountStatus, local map[string]profile.Profile,
	remote map[string]aub.PublishedProfile,
) []syncOperation {
	client := s.aubClient()
	catalog, _ := s.catalog()
	operations := []syncOperation{}
	for _, row := range status.Profiles {
		switch row.State {
		case "to_upload":
			operation := syncOperation{outcome: syncOutcome{ID: row.ID, Name: row.Name, Action: "upload", Result: "planned"}}
			preview, err := publish.PreviewOf(local[row.ID])
			if err != nil {
				operation.outcome.Result, operation.outcome.Error = resultOf(err)
			} else {
				operation.upload = &preview
				operation.outcome.Version, operation.outcome.Digest = preview.Version, preview.Digest
				operation.outcome.Disclosures = len(preview.Disclosures)
				for _, disclosure := range preview.Disclosures {
					operation.identity = append(operation.identity, fmt.Sprintf("discloses %+v", disclosure))
				}
			}
			operations = append(operations, operation)
		case "to_download":
			operation := syncOperation{outcome: syncOutcome{ID: row.ID, Name: row.Name, Action: "download", Result: "planned"}}
			// The version the account's state named — never "whatever is
			// latest when the plan is made". A version published in between
			// changes the state, and with it the review.
			plan, err := publish.PlanInstall(ctx, client, catalog, remote[row.ID].ID, row.Remote)
			if err != nil {
				operation.outcome.Result, operation.outcome.Error = resultOf(err)
			} else {
				operation.install = &plan
				operation.outcome.Version, operation.outcome.Digest = plan.Version.Version, plan.Digest
				for _, permission := range plan.Permissions {
					operation.outcome.Permissions = append(operation.outcome.Permissions, permission.Summary)
					operation.identity = append(operation.identity, fmt.Sprintf("asks %+v", permission))
				}
			}
			operations = append(operations, operation)
		case "conflict":
			operations = append(operations, syncOperation{
				outcome:  syncOutcome{ID: row.ID, Name: row.Name, Action: "none", Result: "conflict", Error: row.Reason},
				identity: []string{"local " + row.Local, "account " + row.Remote},
			})
		}
	}
	return operations
}

// reviewIdentity names a review: whose account, on which server, and every
// operation with its direction, version, content digest, disclosures and
// permissions — and an operation that could not be planned, with why. Two
// reviews with one identity would send and install the same bytes with the
// same approvals. Anything else is a different review.
func reviewIdentity(account, server string, operations []syncOperation) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "aucom.sync-review/1\n%q %q\n", account, server)
	for _, operation := range operations {
		outcome := operation.outcome
		fmt.Fprintf(hash, "%q %q %q %q %q %q %q\n", outcome.ID, outcome.Action, outcome.Result,
			outcome.Version, outcome.Digest, outcome.Error, operation.identity)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s *Server) handleAccountSync(w http.ResponseWriter, r *http.Request) {
	var request syncRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !request.Confirmed && !request.DryRun {
		writeError(w, http.StatusBadRequest, errors.New("sync needs the confirmation the review asks for; nothing was sent or installed"))
		return
	}
	status, local, remote := s.accountState(r.Context())
	if !status.SignedIn {
		writeError(w, http.StatusUnauthorized, errors.New("sign in to sync with your Auto-Pigeon account"))
		return
	}
	if !status.Online {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("your Auto-Pigeon account cannot be reached right now: %s", status.OfflineWhy))
		return
	}

	// The whole plan, before the first write.
	operations := s.planSync(r.Context(), status, local, remote)
	identity := reviewIdentity(status.Email, status.ServerLabel, operations)
	outcomes := make([]syncOutcome, 0, len(operations))
	for _, operation := range operations {
		outcomes = append(outcomes, operation.outcome)
	}
	if request.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{"outcomes": outcomes, "status": status, "reviewed": identity})
		return
	}
	// A confirmation is for the review it names. No review named, or a review
	// of another list — a document edited under the same version, a version
	// published meanwhile, another account signed in: nothing is sent,
	// installed or approved, and the answer is the review as it stands.
	if request.Reviewed != identity {
		why := "the profiles changed since the review; nothing was sent or installed"
		if request.Reviewed == "" {
			why = "a sync is confirmed against the review it was read from; nothing was sent or installed"
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": why, "changed": true, "review_required": request.Reviewed == "",
			"outcomes": outcomes, "status": status, "reviewed": identity,
		})
		return
	}

	client := s.aubClient()
	profilesDir, err := s.profilesDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	bindingsPath, err := s.bindingsPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Execute the plans that were reviewed, as they are held: the previewed
	// bytes are what is published and the served, digest-checked bytes are
	// what is installed. Nothing is looked up again.
	for index, operation := range operations {
		switch {
		case operation.upload != nil:
			_, err := publish.Publish(r.Context(), client, *operation.upload, aub.PublishedPrivate, true)
			outcomes[index].Result, outcomes[index].Error = resultOf(err)
		case operation.install != nil:
			// The person read these permissions in the review and pressed
			// Sync profiles: that is the approval Apply records, for this
			// machine. The listing is one of the account's own (`Mine`), so it
			// may replace what this person wrote here under the same id
			// (NEW_353A).
			_, err := publish.Apply(*operation.install, publish.InstallPaths{Profiles: profilesDir, Bindings: bindingsPath, OwnAccount: true}, true)
			outcomes[index].Result, outcomes[index].Error = resultOf(err)
		}
	}
	after, _, _ := s.accountState(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"outcomes": outcomes, "status": after})
}

func resultOf(err error) (string, string) {
	if err != nil {
		return "failed", err.Error()
	}
	return "done", ""
}

func offlineReason(err error) string {
	var apiErr *aub.APIError
	if errors.As(err, &apiErr) {
		// The legal gate is not a lost sign-in: signing in again would not
		// help, and the page must not say it would (NEW_360 F2; seen live on
		// the account status in NEW_508A, which said exactly that).
		if apiErr.LegalAcceptanceRequired() {
			return "the Terms or the notices changed, and this account has not accepted the current version yet"
		}
		if apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden {
			return "your session has ended; sign in again"
		}
	}
	return "the server did not answer"
}

// blockedBy is the reason code of a refusal only the person can lift, or
// empty: the page offers the acceptance page for it rather than a sign-in.
func blockedBy(err error) string {
	var apiErr *aub.APIError
	if errors.As(err, &apiErr) && apiErr.LegalAcceptanceRequired() {
		return aub.ReasonLegalAcceptanceRequired
	}
	return ""
}

func sortProfiles(rows []accountProfile) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
}
