package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/acquire"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Verified acquisition, over HTTP.
//
// Until AUCOM/AUT 246I there was no route here at all: `companion acquire` was a
// CLI verb, so the only way to obtain a compiler through the page was to be told
// to go and find one. The page's own words for that were "This stage's program
// is not set up on this machine yet. Say where it is, once, in Profiles." —
// which named neither the stage nor the program, and sent somebody to a form
// when the Companion could have fetched and verified the thing itself.
//
// This is an ADAPTER and not a second updater. Every decision — which version,
// which platform, the signature chain, the size, the digest, the sticky
// revocations, the serial ratchet, the licence acknowledgement, the cache —
// stays in internal/acquire, which is the one place that knows them, and
// [acquire.Acquirer.Resolve] already says in as many words that its callers are
// "the CLI, the server, whatever writes a binding". A second implementation here
// would be a second thing to keep in step with the verification chain, and the
// copy that drifts is always the one that forgot a check.
//
// It writes no grant. Recording WHERE a program is says nothing about whether
// the user approved what it does; internal/approval.Service remains the one
// writer of a profile.Grant, and an existing grant survives here only while the
// document's digest is unchanged — see internal/cli's writeBinding, whose rules
// these mirror.

// acquireOptions builds the acquirer's configuration the way the CLI's
// acquirePaths does, so the two processes read and write the same cache, the
// same trust state and the same licence record.
//
// s.paths.ConfigDir is the test seam, matching every other path on this server:
// set, everything hangs off it; empty, the user's real configuration directory.
func (s *Server) acquireOptions() (acquire.Options, error) {
	settings := s.config()
	options := acquire.Options{Offline: config.Offline()}
	if dir := s.paths.ConfigDir; dir != "" {
		options.CacheDir = filepath.Join(dir, "packages")
		options.StatePath = filepath.Join(dir, "catalog-state.json")
		options.AcceptancePath = filepath.Join(dir, "license-acceptance.json")
	} else {
		cacheDir, err := settings.ToolCache()
		if err != nil {
			return options, err
		}
		statePath, err := config.CatalogStatePath()
		if err != nil {
			return options, err
		}
		acceptancePath, err := config.LicenseAcceptancePath()
		if err != nil {
			return options, err
		}
		options.CacheDir, options.StatePath, options.AcceptancePath = cacheDir, statePath, acceptancePath
	}
	// A missing address or anchor file is not an error: the offer below is
	// exactly the place that difference is reported, in words, rather than as a
	// failure to construct anything.
	if url, err := settings.Catalog(); err == nil {
		options.CatalogURL = url
	}
	if anchors, err := settings.CatalogAnchors(); err == nil {
		options.AnchorsPath = anchors
	}
	return options, nil
}

// acquireOffer is what the page needs in order to decide which actions to show.
//
// Available is one field and not four, for the same reason aue.Provenance
// records Verified in one place: a caller that has to compute "can I offer this"
// from the anchors path, the offline flag, the platform list and a catalogue
// error is a caller that will one day compute it wrong.
type acquireOffer struct {
	// Available says whether "Download and set up" can be offered at all.
	Available bool `json:"available"`
	// Reason is the CONCRETE condition when it cannot be, in the user's words.
	// 246I: "If download is unavailable/offline, explain that concrete
	// condition and retain the existing-folder route."
	Reason string `json:"reason,omitempty"`
	// Folder is always true: choosing a folder needs no network, no catalogue
	// and no anchor, so it is the route that always survives.
	Folder bool `json:"folder"`
	// Hint is the profile's own words for what to point at, for that route.
	Hint string `json:"hint,omitempty"`

	Package          string `json:"package,omitempty"`
	Name             string `json:"name,omitempty"`
	Version          string `json:"version,omitempty"`
	Size             int64  `json:"size,omitempty"`
	License          string `json:"license,omitempty"`
	Signer           string `json:"signer,omitempty"`
	Source           string `json:"source,omitempty"`
	NeedsAcceptance  bool   `json:"needs_acceptance,omitempty"`
	AlreadyInstalled bool   `json:"already_installed,omitempty"`
}

// managedOption finds the verified-download route this profile offers for the
// machine that is running, and says why there is none when there is none.
func managedOption(tool *profile.ToolProfile) (profile.AcquisitionOption, string) {
	platform := acquire.CurrentPlatform()
	offered := false
	for _, option := range tool.Acquisition {
		if option.Mode != profile.AcquireManagedDownload {
			continue
		}
		offered = true
		if len(option.Platforms) > 0 && !containsAcquirePlatform(option.Platforms, platform) {
			continue
		}
		return option, ""
	}
	if offered {
		return profile.AcquisitionOption{}, fmt.Sprintf(
			"%s publishes no verified build for %s.", tool.Meta.Name, platform)
	}
	return profile.AcquisitionOption{}, fmt.Sprintf(
		"%s does not offer a verified download.", tool.Meta.Name)
}

// folderHint is what the profile tells the user to point at for the local route.
func folderHint(tool *profile.ToolProfile) string {
	for _, option := range tool.Acquisition {
		if option.Mode == profile.AcquireUserPath && option.Hint != "" {
			return option.Hint
		}
	}
	return ""
}

func containsAcquirePlatform(platforms []profile.Platform, want profile.Platform) bool {
	for _, platform := range platforms {
		if platform == want {
			return true
		}
	}
	return false
}

// toolProfile is the profile with an id, refused if it is not a tool.
func (s *Server) toolProfile(id string) (*profile.ToolProfile, profile.Profile, error) {
	entry, _, err := s.profileEntry(id)
	if err != nil {
		return nil, nil, err
	}
	tool, isTool := entry.Profile.(*profile.ToolProfile)
	if !isTool {
		return nil, nil, fmt.Errorf("%s is not a tool profile, so it has no programs to obtain", id)
	}
	return tool, entry.Profile, nil
}

// handleAcquireOffer answers "what can this machine do about this tool".
//
// It downloads nothing and writes nothing, so the page may ask it at the moment
// a build is blocked — which is the moment the user actually wants to know.
func (s *Server) handleAcquireOffer(w http.ResponseWriter, r *http.Request) {
	tool, _, err := s.toolProfile(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	offer := acquireOffer{Folder: true, Hint: folderHint(tool)}

	option, why := managedOption(tool)
	if why != "" {
		offer.Reason = why
		writeJSON(w, http.StatusOK, offer)
		return
	}
	offer.Package = option.CatalogPackage

	options, err := s.acquireOptions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// The conditions worth naming before any network call, because each is
	// about this machine's configuration rather than about the catalogue. The
	// first is the one the Windows host of 246I actually met.
	if options.AnchorsPath == "" {
		offer.Reason = "This computer has no catalogue trust anchor configured, so a download cannot be verified — and an unverified one is never offered instead."
		writeJSON(w, http.StatusOK, offer)
		return
	}
	if options.CatalogURL == "" {
		offer.Reason = "This computer has no acquisition catalogue address configured."
		writeJSON(w, http.StatusOK, offer)
		return
	}
	acquirer, err := acquire.New(options)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if acquirer.Offline() {
		offer.Reason = "This computer is in offline mode, so nothing is downloaded. Anything already installed still works."
		writeJSON(w, http.StatusOK, offer)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	plan, err := acquirer.Plan(ctx, option.CatalogPackage, "")
	if err != nil {
		// A catalogue that cannot be reached, verified or trusted is a concrete
		// condition too, and its own sentence is the best one available.
		offer.Reason = err.Error()
		writeJSON(w, http.StatusOK, offer)
		return
	}
	offer.Available = true
	offer.Name = plan.Package.Name
	offer.Version = plan.Package.Version
	offer.Size = plan.Artifact.Size
	offer.License = plan.Package.License.SPDX
	offer.Signer = plan.Signer
	offer.Source = plan.URL // already redacted by acquire; see catalog.RedactURL
	offer.NeedsAcceptance = plan.NeedsAcceptance
	offer.AlreadyInstalled = plan.AlreadyInstalled
	writeJSON(w, http.StatusOK, offer)
}

// acquireRequest is what the page sends to actually obtain the programs.
type acquireRequest struct {
	// Version pins a catalogue version. Empty means the newest it offers.
	Version string `json:"version,omitempty"`
	// AcceptLicense records that the notice was shown and acknowledged. It is a
	// separate field rather than something implied by asking to download,
	// because a licence nobody was shown is a licence nobody accepted.
	AcceptLicense bool `json:"accept_license,omitempty"`
}

// handleAcquireInstall downloads, verifies, installs and records.
//
// Every one of those verbs but the last happens inside internal/acquire, and the
// last uses the canonical local-binding store under its cross-process lock — so
// a resolution running here cannot discard a grant the CLI recorded a moment
// ago, or the other way round.
func (s *Server) handleAcquireInstall(w http.ResponseWriter, r *http.Request) {
	var request acquireRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	tool, document, err := s.toolProfile(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	option, why := managedOption(tool)
	if why != "" {
		writeError(w, http.StatusBadRequest, errors.New(why))
		return
	}

	options, err := s.acquireOptions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if options.AnchorsPath == "" {
		writeError(w, http.StatusConflict, errors.New(
			"this computer has no catalogue trust anchor configured, so a download cannot be verified; "+
				"choose a folder that already holds the programs instead"))
		return
	}
	acquirer, err := acquire.New(options)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// A download plus an unpack, over a link nobody promised anything about.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()

	plan, err := acquirer.Plan(ctx, option.CatalogPackage, request.Version)
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	if plan.NeedsAcceptance && !request.AcceptLicense {
		// 409 and not 400: nothing is wrong with the request, there is a
		// decision outstanding. The notice travels with the refusal, so the
		// page has something to show rather than something to go and fetch.
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":            "this package's licence notice has not been acknowledged on this computer",
			"needs_acceptance": true,
			"notice":           plan.Text(),
			"license":          plan.Package.License.SPDX,
		})
		return
	}
	if plan.NeedsAcceptance {
		if _, err := acquirer.Accept(ctx, option.CatalogPackage, request.Version); err != nil {
			writeError(w, jobStatus(err), err)
			return
		}
	}

	result, err := acquirer.Resolve(ctx, acquire.Request{
		Option:      option,
		Executables: tool.Executables,
		Version:     request.Version,
	})
	if err != nil {
		// Whatever refused — a wrong digest, a wrong size, a revoked artifact,
		// an expired catalogue, an interrupted transfer — refused before
		// anything was published, and its own sentence is the record.
		writeError(w, jobStatus(err), err)
		return
	}

	updated, err := s.recordAcquisition(document, result)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"binding":     updated,
		"mode":        result.Mode,
		"description": result.Description,
		"tool_root":   result.ToolRoot,
		"executables": result.Executables,
	})
}

// recordAcquisition writes where the programs are, and nothing else.
//
// These rules are internal/cli's writeBinding, deliberately: a binding whose
// document changed is rebuilt rather than edited, because a grant is against
// bytes and the bytes moved; the tool-install root comes from the resolution;
// and an older pinned install is kept rather than replaced, because the
// collector reads that list and "the last one wins" is not a decision anybody
// made.
func (s *Server) recordAcquisition(document profile.Profile, result *acquire.Result) (binding.LocalBinding, error) {
	path, err := s.bindingsPath()
	if err != nil {
		return binding.LocalBinding{}, err
	}
	digest, err := profile.Digest(document)
	if err != nil {
		return binding.LocalBinding{}, err
	}
	meta := document.Metadata()

	var written binding.LocalBinding
	if _, err := binding.Update(path, func(set *binding.Set) error {
		local, existed := set.Find(meta.ID)
		if !existed || local.ProfileDigest != digest {
			local = binding.LocalBinding{Trust: profile.TrustLocal}
		}
		local.ProfileID = meta.ID
		local.ProfileVersion = meta.Version
		local.ProfileDigest = digest
		if local.Trust == "" {
			local.Trust = profile.TrustLocal
		}
		local.Acquisition = result.Mode
		local.Executables = result.Executables
		if result.ToolRoot != "" {
			if local.Roots == nil {
				local.Roots = map[string]string{}
			}
			local.Roots[profile.RootToolInstall] = result.ToolRoot
		}
		if result.Install != nil {
			local.Installs = pinAcquiredInstall(local.Installs, binding.PinnedInstall{
				PackageID: result.Install.PackageID,
				Version:   result.Install.Version,
				Digest:    result.Install.Digest,
				Platform:  result.Install.Platform.String(),
				PinnedAt:  time.Now().UTC(),
			})
			local.ResolvedVersion = result.Install.Version
		}
		local.UpdatedAt = time.Now().UTC()
		written = local
		return set.Put(local)
	}); err != nil {
		return binding.LocalBinding{}, err
	}
	return written, nil
}

// pinAcquiredInstall puts the newly resolved download first and keeps the rest.
func pinAcquiredInstall(existing []binding.PinnedInstall, current binding.PinnedInstall) []binding.PinnedInstall {
	out := []binding.PinnedInstall{current}
	for _, install := range existing {
		if install.Digest != current.Digest {
			out = append(out, install)
		}
	}
	return out
}
