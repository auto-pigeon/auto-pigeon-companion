package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
)

// The textures an editor's Quake 1 leak test compiles with (NEW_357).
//
// Found live (`NEW_353A`, F-LEAKWAD): Build & Run of a saved revision gave qbsp
// the map's verified AUB texture bundle as its `content_root`, and the SAME
// revision asked for through the editor's Test in Companion got nothing — the
// page sends the map as an `aub:` input and has no folder to send, so qbsp
// logged `archive 'new331.wad' not found`, opened no WAD, and the result was
// rightly labelled incomplete. The leak verdict was right; the run was not the
// whole compile.
//
// So the build start fetches the bundle for exactly the revision the request
// pinned, through the one owner Build & Run uses (verifiedTextureBundle), and
// gives the run its own copy. What it never does is look anywhere else: no
// folder the page typed, no WAD of the same name somewhere on this machine. A
// bundle that cannot be had is a refusal before anything runs, and a bundle AUB
// sent incomplete is compiled as it is, so the compiler's own warnings say what
// was missing.

// leakTexturesPrefix names the job-owned copies in the builds directory.
const leakTexturesPrefix = "leak-textures-"

// stageLeakTextures copies the verified texture bundle of one pinned map
// revision into a directory this build owns, and says where it came from.
//
// Each file is hashed again while it is copied, against the digest the cache
// verified it at: a cached path on disk is not a reason to trust what it holds
// now. The caller removes the directory when the build is over.
func (s *Server) stageLeakTextures(ctx context.Context, source build.SourceRef, mapPath string) (
	string, build.RootSource, error) {
	if source.AssetType != aub.AssetTypeMap || source.AssetID == "" || source.Revision < 1 {
		return "", build.RootSource{}, errors.New("this leak test names no saved map revision to fetch textures for")
	}
	entry, err := s.verifiedTextureBundle(ctx, source.AssetID, source.Revision)
	if err != nil {
		return "", build.RootSource{}, leakTextureError(source.Revision, err)
	}
	if entry.Receipt.MapID != source.AssetID || entry.Receipt.Revision != source.Revision {
		return "", build.RootSource{}, fmt.Errorf("the texture bundle found is for map %s revision %d, not this request's revision %d",
			entry.Receipt.MapID, entry.Receipt.Revision, source.Revision)
	}
	buildsDir, err := s.buildsDir()
	if err != nil {
		return "", build.RootSource{}, err
	}
	dir, err := os.MkdirTemp(ensureDir(buildsDir), leakTexturesPrefix)
	if err != nil {
		return "", build.RootSource{}, fmt.Errorf("preparing this leak test's texture folder: %w", err)
	}
	staged := false
	defer func() {
		if !staged {
			_ = os.RemoveAll(dir)
		}
	}()
	for _, file := range entry.Receipt.Files {
		if err := ctx.Err(); err != nil {
			return "", build.RootSource{}, err
		}
		relative := filepath.FromSlash(file.Path)
		if !filepath.IsLocal(relative) {
			return "", build.RootSource{}, fmt.Errorf("the texture bundle names %q, which is not a file inside it", file.Path)
		}
		if err := copyVerified(filepath.Join(entry.ContentRoot, relative), filepath.Join(dir, relative), file); err != nil {
			return "", build.RootSource{}, err
		}
	}
	// A WAD the map declares under a folder (`gfx/metal.wad`) is also where
	// it says, exactly as Build & Run places it.
	if _, err := playrun.PlaceDeclaredWADs(dir, mapPath); err != nil {
		return "", build.RootSource{}, fmt.Errorf("placing the map's WADs where it declares them: %w", err)
	}
	staged = true

	return dir, build.RootSource{Kind: build.RootFromTextureBundle, Bundle: bundleResult(entry, s.backendAddress()).Ref}, nil
}

// leakTextureRoots gives a Quake 1 leak build the texture root of its pinned
// revision, replacing any folder the page sent: a path a browser supplied is
// not where an editor's request reads its textures from. It returns the
// job-owned directory, "" when the pinned pipeline reads no texture folder.
func (s *Server) leakTextureRoots(ctx context.Context, request buildRequestBody, source build.SourceRef, mapPath string,
	roots map[string]string, rootSources map[string]build.RootSource, log io.Writer) (
	map[string]string, map[string]build.RootSource, string, error) {
	declares, err := s.pipelineDeclaresRoot(request.Pipeline, profile.RootContent)
	if err != nil {
		return nil, nil, "", err
	}
	if !declares {
		// Said, as Build & Run says it: the compiler of this pipeline is given
		// no texture folder, and its own warnings will show what that costs.
		fmt.Fprintln(log, "Textures: no step of this leak-test pipeline reads a texture folder, so the map's textures "+
			"are not given to the compiler.")
		return roots, rootSources, "", nil
	}
	// The editor is told the build is being prepared while the textures come.
	if pending := s.pendingLeakRequest(request.LeakRequestID); pending != nil {
		s.reportLeakStatus(*pending, leakBuilding, "starting", "")
	}
	dir, rootSource, err := s.stageLeakTextures(ctx, source, mapPath)
	if err != nil {
		return nil, nil, "", err
	}
	_, typed := roots[profile.RootContent]
	if roots == nil {
		roots = map[string]string{}
	}
	if rootSources == nil {
		rootSources = map[string]build.RootSource{}
	}
	roots[profile.RootContent], rootSources[profile.RootContent] = dir, rootSource
	bundle := rootSource.Bundle
	names := make([]string, 0, len(bundle.Files))
	for _, file := range bundle.Files {
		names = append(names, file.Path)
	}
	fmt.Fprintf(log, "Textures: the texture bundle of saved revision %d (%d file(s): %s), verified and copied for this "+
		"leak test.\n", bundle.Revision, len(names), strings.Join(names, ", "))
	if typed {
		fmt.Fprintln(log, "Textures: the folder chosen on this page was not used; an editor's leak test reads the "+
			"textures of the revision it asked for.")
	}
	if !bundle.CompilerReady {
		fmt.Fprintf(log, "Textures: your account says this bundle is not complete enough to compile (%s); the "+
			"compiler's warnings below name what it could not find.\n", strings.Join(bundle.CompilerRefusals, "; "))
	}

	return roots, rootSources, dir, nil
}

// previewLeakTextureRoots is leakTextureRoots for the command preview: the
// same bundle, from the same owner, named as the texture root. Nothing is
// copied — a preview runs nothing — so the path shown is the verified cache
// entry; the build gives the compiler its own copy of exactly those files.
func (s *Server) previewLeakTextureRoots(ctx context.Context, request buildRequestBody, source build.SourceRef,
	roots map[string]string, rootSources map[string]build.RootSource) (
	map[string]string, map[string]build.RootSource, error) {
	declares, err := s.pipelineDeclaresRoot(request.Pipeline, profile.RootContent)
	if err != nil || !declares {
		return roots, rootSources, err
	}
	// The editor is not told anything here: reviewing is not building.
	entry, err := s.verifiedTextureBundle(ctx, source.AssetID, source.Revision)
	if err != nil {
		return nil, nil, leakTextureError(source.Revision, err)
	}
	if roots == nil {
		roots = map[string]string{}
	}
	if rootSources == nil {
		rootSources = map[string]build.RootSource{}
	}
	roots[profile.RootContent] = entry.ContentRoot
	rootSources[profile.RootContent] = build.RootSource{Kind: build.RootFromTextureBundle,
		Bundle: bundleResult(entry, s.backendAddress()).Ref}

	return roots, rootSources, nil
}

// leakTextureStatus is the answer to a leak build whose textures could not be
// had: sign in again when the account refused the session, otherwise a
// failure of the account server or the cache, which a retry may fix.
func leakTextureStatus(err error) int {
	var refusal *aub.TextureExportRefusal
	if errors.As(err, &refusal) && (refusal.Status == 401 || refusal.Status == 403) {
		return refusal.Status
	}
	return aubStatus(err)
}

// removeLeakTextures removes a job-owned texture copy; "" is nothing.
func removeLeakTextures(dir string) {
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// copyVerified copies one bundle member, refusing it unless the bytes copied
// are the size and the digest it was verified at. Nothing is left behind on a
// refusal: the caller removes the whole directory.
func copyVerified(from, to string, member texturebundle.Member) error {
	source, err := os.Open(from)
	if err != nil {
		return fmt.Errorf("the cached texture %s cannot be read: %w", member.Path, err)
	}
	defer source.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(target, hash), io.LimitReader(source, member.Bytes+1))
	if closeErr := target.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if written != member.Bytes || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), member.SHA256) {
		return fmt.Errorf("the cached texture %s no longer matches what was verified; it was not used", member.Path)
	}

	return nil
}

// leakTextureError says why the textures could not be had, in the request's
// terms: the first line is the sentence the page shows, naming the bundle and
// what to do; the lines under it are the codes, for Technical details. The
// account server's address and its routes are never repeated — they are the
// reader's own, and an internal route is not something to act on (NEW_357).
func leakTextureError(revision int, err error) error {
	subject := fmt.Sprintf("This leak test needs the texture bundle of saved revision %d from your account", revision)
	var api *aub.APIError
	var refusal *aub.TextureExportRefusal
	var transport *url.Error
	switch {
	case errors.As(err, &api) && api.StatusCode == http.StatusUnauthorized:
		return &leakTextureFailure{cause: err, text: subject + ", and your account no longer accepts this Companion's " +
			"sign-in, so nothing was compiled. Sign in again, then press Check again.\n" +
			fmt.Sprintf("texture export: HTTP %d%s", api.StatusCode, reasonSuffix(api.Reason))}
	case errors.As(err, &api):
		return &leakTextureFailure{cause: err, text: subject + ", and your account refused to send it, so nothing " +
			"was compiled.\n" + fmt.Sprintf("texture export: HTTP %d%s", api.StatusCode, reasonSuffix(api.Reason))}
	case errors.As(err, &refusal):
		return &leakTextureFailure{cause: err, text: subject + ", and your account refused to send it, so nothing " +
			"was compiled. " + refusal.Detail + "\n" + fmt.Sprintf("texture export: HTTP %d%s", refusal.Status, reasonSuffix(refusal.Reason))}
	case errors.As(err, &transport):
		return &leakTextureFailure{cause: err, text: subject + ", and your account's server could not be reached, so " +
			"nothing was compiled. Press Check again when it is back.\n" + transport.Err.Error()}
	}
	return &leakTextureFailure{cause: err, text: subject + ", and it could not be fetched, so nothing was compiled.\n" + err.Error()}
}

// leakTextureFailure is leakTextureError's answer: its own text, and the
// cause kept for the status it maps to.
type leakTextureFailure struct {
	text  string
	cause error
}

func (e *leakTextureFailure) Error() string { return e.text }
func (e *leakTextureFailure) Unwrap() error { return e.cause }

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}

// sweepLeakTextures removes job-owned texture copies a stopped Companion left:
// a copy belongs to a build of the process that made it, and none of those is
// running now.
func (s *Server) sweepLeakTextures(olderThan time.Duration) {
	dir, err := s.buildsDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), leakTexturesPrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < olderThan {
			continue
		}
		if os.RemoveAll(filepath.Join(dir, entry.Name())) == nil {
			removed++
		}
	}
	if removed > 0 {
		s.logf("leak test: removed %d texture folder(s) a stopped Companion left", removed)
	}
}
