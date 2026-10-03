package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

const leakPipelineID = "auto-pigeon.q1.leak-test"

// All routes are behind Server.guard. The URI handler only records an intent;
// this review reads the user's normal AUB session and starts no program.
func (s *Server) leakTestAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/leak-test/pending":           s.handleLeakTestPending,
		"GET /api/v1/leak-test/request":           s.handleLeakTestRequest,
		"POST /api/v1/leak-test/reviewing":        s.handleLeakTestReviewing,
		"POST /api/v1/leak-test/dismiss":          s.handleLeakTestDismiss,
		"GET /api/v1/leak-test/runs/{id}/result":  s.handleLeakTestResult,
		"GET /api/v1/leak-test/runs/{id}/return":  s.handleLeakTestReturn,
		"POST /api/v1/leak-test/runs/{id}/return": s.handleLeakTestReturnRetry,
	}
}

func (s *Server) handleLeakTestPending(w http.ResponseWriter, r *http.Request) {
	dir, err := s.configDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	request, received, err := leakintent.Read(leakintent.Path(dir), time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if request == nil {
		writeJSON(w, http.StatusOK, map[string]any{"pending": false})
		return
	}
	// A page names the request it is asking about. When another one replaced
	// it meanwhile, the answer is "replaced" and nothing is resolved: resolving
	// the newer request here would hand the page facts about B to show under A.
	if named := r.URL.Query().Get("request_id"); named != "" && named != request.RequestID {
		writeJSON(w, http.StatusOK, map[string]any{"pending": true, "replaced": true,
			"request_id": request.RequestID, "asset_id": request.AssetID, "revision": request.Revision, "received_at": received})
		return
	}

	s.adoptSessionFromDisk()
	client := s.aubClient()
	if client == nil || !client.Authenticated() {
		writeJSON(w, http.StatusOK, map[string]any{"pending": true, "sign_in_required": true,
			"request_id": request.RequestID, "asset_id": request.AssetID, "revision": request.Revision,
			"content_sha256": request.ContentSHA256, "received_at": received})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	// A new save after the editor's click makes this request stale. Refuse it:
	// resolving @current and then submitting @current would compile different bytes.
	revision, err := client.Revision(ctx, aub.AssetTypeMap, request.AssetID, aub.CurrentRevision)
	if err != nil {
		writeError(w, aubStatus(err), err)
		return
	}
	if revision.AssetID != request.AssetID || revision.AssetType != aub.AssetTypeMap ||
		revision.Number != request.Revision || revision.ContentSHA256 != request.ContentSHA256 ||
		!revision.Immutable || revision.ID == "" {
		s.reportLeakStatus(*request, leakBlocked, "the saved map revision changed since this request", "")
		writeError(w, http.StatusConflict, errors.New("the saved map revision changed or cannot be pinned; ask the editor again"))
		return
	}
	var source string
	for _, file := range revision.Files {
		if strings.EqualFold(filepath.Ext(file.Path), ".apmap") {
			if source != "" {
				writeError(w, http.StatusConflict, errors.New("this revision has more than one APMap source"))
				return
			}
			source = file.Path
		}
	}
	if source == "" {
		writeError(w, http.StatusConflict, errors.New("this revision has no APMap source"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pending": true, "received_at": received, "pipeline": leakPipelineID,
		"request_id": request.RequestID, "name": strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)),
		"asset_id": request.AssetID, "revision": revision.Number, "revision_id": revision.ID,
		"content_sha256": revision.ContentSHA256, "files": revision.Files,
		"source_ref": fmt.Sprintf("aub:map/%s@%s#%s", request.AssetID, revision.ID, source),
	})
}

func (s *Server) matchesPendingLeakRequest(requestID string, source build.SourceRef) bool {
	if requestID == "" || source.AssetType != aub.AssetTypeMap || source.RevisionID == "" || !source.Refetchable {
		return false
	}
	dir, err := s.configDir()
	if err != nil {
		return false
	}
	request, _, err := leakintent.Read(leakintent.Path(dir), time.Now().UTC())
	return err == nil && request != nil && request.RequestID == requestID &&
		request.AssetID == source.AssetID && request.Revision == source.Revision &&
		request.ContentSHA256 == source.ContentSHA256
}

// The build itself has already finished when this runs. A failed leaktest can
// still have the compiler's pointfile, so terminal failure is a deliverable.
func (s *Server) publishLeakResult(buildID, requestID string) error {
	result, err := s.buildLeakResult(buildID)
	if err != nil {
		return err
	}
	s.adoptSessionFromDisk()
	client := s.aubClient()
	if client == nil || !client.Authenticated() {
		return errors.New("sign in to AUB again to return this result; the file download remains available")
	}
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err = client.PublishLeakResult(ctx, requestID, result)
		cancel()
		if err == nil {
			return nil
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
	}
	return err
}

// handleLeakTestDismiss forgets the request a person dismissed — that one. A
// page names the request it was showing, so a newer link that arrived while
// its card was on screen is not erased by a click meant for the older one. A
// body without an id is the older form, and forgets whatever is pending.
func (s *Server) handleLeakTestDismiss(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequestID string `json:"request_id"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &body) {
		return
	}
	dir, err := s.configDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	path, now := leakintent.Path(dir), time.Now().UTC()
	if body.RequestID == "" {
		if err = leakintent.Dismiss(path, now); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"dismissed": true})
		return
	}
	dismissed, err := leakintent.Consume(path, body.RequestID, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if dismissed != nil {
		s.reportLeakStatus(*dismissed, leakDismissed, "", "")
	}
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": dismissed != nil})
}

type leakResult struct {
	SchemaVersion   string `json:"schema_version"`
	BuildID         string `json:"build_id"`
	MapID           string `json:"map_id"`
	RevisionID      string `json:"revision_id"`
	Revision        int    `json:"revision"`
	ContentSHA256   string `json:"content_sha256"`
	CompilerVersion string `json:"compiler_version,omitempty"`
	BuildState      string `json:"build_state"`
	CompileExitCode *int   `json:"compile_exit_code,omitempty"`
	Pointfile       string `json:"pointfile,omitempty"`
	PointfileSHA256 string `json:"pointfile_sha256,omitempty"`
	Log             string `json:"log,omitempty"`
	LogSHA256       string `json:"log_sha256,omitempty"`
}

func (s *Server) handleLeakTestResult(w http.ResponseWriter, r *http.Request) {
	result, err := s.buildLeakResult(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", result.BuildID+"-leak-result.json"))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) buildLeakResult(id string) (leakResult, error) {
	dir, err := s.buildsDir()
	if err != nil {
		return leakResult{}, err
	}
	manifest, err := build.Find(dir, id)
	if err != nil {
		return leakResult{}, err
	}
	if manifest.Pipeline.ID != leakPipelineID {
		return leakResult{}, errors.New("this is not a leak-test build")
	}
	if !manifest.State.Terminal() {
		return leakResult{}, errors.New("the leak test is still running")
	}
	var source *build.SourceRef
	for _, input := range manifest.Inputs {
		if input.Name == "source_map" {
			source = input.Source
			break
		}
	}
	if source == nil || source.AssetType != aub.AssetTypeMap || source.AssetID == "" ||
		source.RevisionID == "" || source.Revision < 1 || len(source.ContentSHA256) != 64 || !source.Refetchable {
		return leakResult{}, errors.New("the build has no pinned AUB map source")
	}
	result := leakResult{SchemaVersion: "aucom.leak-result/1.0", BuildID: manifest.BuildID,
		MapID: source.AssetID, RevisionID: source.RevisionID, Revision: source.Revision, ContentSHA256: source.ContentSHA256,
		BuildState: string(manifest.State)}
	for _, tool := range manifest.Tools {
		if tool.ToolVersion != "" {
			result.CompilerVersion = tool.ToolVersion
			break
		}
	}
	for _, step := range manifest.Steps {
		if step.ID == "compile" {
			result.CompileExitCode = step.ExitCode
			break
		}
	}
	for _, output := range manifest.Outputs {
		var limit int64
		switch output.Name {
		case "pts":
			limit = 2 << 20
		case "compile_log":
			limit = 16 << 20
		default:
			continue
		}
		if output.Missing || output.Path == "" {
			continue
		}
		content, err := readLeakOutput(dir, manifest.BuildID, output, limit)
		if err != nil {
			return leakResult{}, err
		}
		if output.Name == "pts" {
			result.Pointfile, result.PointfileSHA256 = content, strings.TrimPrefix(output.SHA256, "sha256:")
		}
		if output.Name == "compile_log" {
			result.Log, result.LogSHA256 = content, strings.TrimPrefix(output.SHA256, "sha256:")
		}
	}
	if result.Log == "" {
		return leakResult{}, errors.New("this build has no compiler log; there is no diagnostic to import")
	}
	return result, nil
}

func readLeakOutput(buildDir, id string, output build.FileRecord, limit int64) (string, error) {
	rootPath := filepath.Join(buildDir, id, "output")
	relative, err := filepath.Rel(rootPath, output.Path)
	if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", errors.New("the recorded compiler output is outside this build")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return "", err
	}
	defer root.Close()
	file, err := root.Open(relative)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return "", errors.New("the compiler output is too large or is not a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(raw)) > limit {
		return "", errors.New("the compiler output exceeds its limit")
	}
	digest := sha256.Sum256(raw)
	if output.SHA256 != "sha256:"+hex.EncodeToString(digest[:]) {
		return "", errors.New("the compiler output no longer matches its recorded digest")
	}
	return string(raw), nil
}
