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

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetref"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetsync"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

// Which pipeline a leak request may run is leakadapter's, keyed by the game the
// pinned revision's own bytes declare. There is no pipeline id here.

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
	// A session that has run out is "signed out" to the person looking at this
	// notice. Found live (`NEW_307W1`): a token that expired a week earlier got
	// past "is a token set", AUB answered 401, and the notice printed the raw
	// refusal with a Retry that could only be refused again — where the one
	// thing that helps is Sign in.
	signIn := func() {
		writeJSON(w, http.StatusOK, map[string]any{"pending": true, "sign_in_required": true,
			"request_id": request.RequestID, "asset_id": request.AssetID, "revision": request.Revision,
			"content_sha256": request.ContentSHA256, "received_at": received})
	}
	if client == nil || !client.Authenticated() || client.SessionExpired(time.Now()) {
		signIn()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	// A new save after the editor's click makes this request stale. Refuse it:
	// resolving @current and then submitting @current would compile different bytes.
	revision, err := client.Revision(ctx, aub.AssetTypeMap, request.AssetID, aub.CurrentRevision)
	if err != nil {
		var refused *aub.APIError
		if errors.As(err, &refused) && refused.Unauthorized() {
			// AUB is the authority on a token that carries no expiry of its own.
			signIn()
			return
		}
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
	// The game is the saved document's own word, read from the pinned bytes.
	// Everything else — the link's hint, whatever pipeline the page had
	// selected — is checked against it.
	game, err := s.savedMapGame(ctx, client, request.AssetID, revision.ID, source)
	if err != nil {
		writeError(w, aubStatus(err), fmt.Errorf("reading this revision's game: %w", err))
		return
	}
	base := map[string]any{
		"pending": true, "received_at": received, "game_profile": game,
		"request_id": request.RequestID, "name": strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)),
		"asset_id": request.AssetID, "revision": revision.Number, "revision_id": revision.ID,
		"content_sha256": revision.ContentSHA256,
	}
	if request.Profile != "" && request.Profile != game {
		s.reportLeakStatus(*request, leakBlocked, leakStageProfileMismatch, "")
		writeError(w, http.StatusConflict, fmt.Errorf(
			"the editor asked for a %s leak test, and this saved revision is a %s map; ask the editor again",
			request.Profile, game))
		return
	}
	adapter, err := leakadapter.ForProfile(game)
	if err != nil {
		// Not Quake 1 by default, and nothing runs: the reader is told which
		// game it is and which ones have a compiler here.
		s.reportLeakStatus(*request, leakBlocked, leakStageUnsupported, "")
		base["unsupported"], base["supported_profiles"] = true, leakadapter.Profiles()
		writeJSON(w, http.StatusOK, base)
		return
	}
	base["compiler"], base["files"] = adapter.Compiler, revision.Files
	base["source_ref"] = fmt.Sprintf("aub:map/%s@%s#%s", request.AssetID, revision.ID, source)
	// The pipeline is the one the user pinned for this game (NEW_310). None
	// pinned, or one that cannot answer this game's leak test any more: the
	// page asks, and nothing is reviewed until it has an answer.
	if pinned := s.pinnedLeakPipeline(game); pinned == "" {
		base["needs_pipeline"] = true
	} else if _, err := s.leakBinding(game, pinned); err != nil {
		base["needs_pipeline"], base["pinned_problem"] = true, err.Error()
	} else {
		base["pipeline"] = pinned
	}
	writeJSON(w, http.StatusOK, base)
}

// errNoLeakLog reports a leak-test build that left no compiler text at all.
// Measured (`Q3_018`): Q3Map2's output is block-buffered when it is not a
// terminal, so a run stopped a few seconds in has printed nothing this program
// ever received. There is then no evidence of any kind — not a log to read,
// not a result to return.
var errNoLeakLog = errors.New("this build has no compiler log; there is no diagnostic to import")

// The two reasons a request is blocked by its game. Tokens, not sentences: the
// editor has a translated line for each.
const (
	leakStageUnsupported     = "unsupported_profile"
	leakStageProfileMismatch = "profile_mismatch"
)

// savedMapGame reads the `game` out of one pinned revision's APMap.
//
// It goes through the asset cache, which checks every file against the digest
// the revision recorded before handing it over — so the game is read from the
// bytes the build will be given, and a revision already cached costs no
// download. The copy made to read it is removed again.
func (s *Server) savedMapGame(ctx context.Context, client *aub.Client, assetID, revisionID, file string) (string, error) {
	store, err := s.assets()
	if err != nil {
		return "", err
	}
	var syncer *assetsync.Syncer
	if client != nil && client.Authenticated() {
		if made, err := assetsync.NewSyncer(ctx, client, store); err == nil {
			syncer = made
		}
	}
	buildsDir, err := s.buildsDir()
	if err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(ensureDir(buildsDir), "leak-review-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	input, err := assetref.Materialize(ctx, store, syncer,
		assetref.Ref{AssetType: aub.AssetTypeMap, AssetID: assetID, Revision: revisionID, File: file}, stage, "source_map")
	if err != nil {
		return "", err
	}
	return assetref.APMapGame(input.Path)
}

// leakBindingForBuild is the one resolver the build start uses, and it is the
// review's: the game the pinned APMap itself declares, and the pipeline the
// user pinned for that game (NEW_310). A request naming any other pipeline is
// refused, and so is one for a game with nothing pinned.
func (s *Server) leakBindingForBuild(pipelineID string, conversion build.Conversion, converted bool) (leakadapter.Binding, error) {
	if !converted || conversion.Game == "" {
		return leakadapter.Binding{}, errors.New("the pinned map source is not an APMap whose game can be read, so no compiler is chosen for it")
	}
	if _, err := leakadapter.ForProfile(conversion.Game); err != nil {
		return leakadapter.Binding{}, err
	}
	pinned := s.pinnedLeakPipeline(conversion.Game)
	if pinned == "" {
		return leakadapter.Binding{}, fmt.Errorf("no leak-test pipeline is pinned for %s maps; choose one in the Companion first", conversion.Game)
	}
	if pinned != pipelineID {
		return leakadapter.Binding{}, fmt.Errorf("this saved revision is a %s map, and its leak test is %q, not %q",
			conversion.Game, pinned, pipelineID)
	}
	return s.leakBinding(conversion.Game, pipelineID)
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

// leakResult is the envelope returned to the editor.
//
// `aucom.leak-result/1.0` is the Quake 1 envelope and is exactly what it was:
// the fields below the line are absent from it. `1.1` (`Q3_018`) names what
// ran — the game, the compiler, the point file's format and direction — and
// carries this program's reading of the run, because the three facts a log
// cannot state about itself (was the BSP there, how did the step end, is the
// line file this run's) are known only here.
//
// Three states travel apart and are never folded into one: how the process
// exited (`compile_exit_code`), how the pipeline ended (`build_state`) and what
// the run says about leaks (`diagnostic.outcome`). A leaked Quake III map is
// exit 0, `failed`, `leak`.
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

	// --- 1.1 ---
	GameProfile        string `json:"game_profile,omitempty"`
	Compiler           string `json:"compiler,omitempty"`
	PointfileFormat    string `json:"pointfile_format,omitempty"`
	PointfileDirection string `json:"pointfile_direction,omitempty"`
	// CompilerSourceSHA256 is the `.map` the compiler was handed — the
	// extractor's conversion of the saved APMap. It is NOT `content_sha256`,
	// which is the saved revision, and the two are never compared.
	CompilerSourceSHA256 string               `json:"compiler_source_sha256,omitempty"`
	Diagnostic           *leakadapter.Verdict `json:"diagnostic,omitempty"`
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
	adapter, binding, isLeakTest := leakBindingFor(manifest)
	if !isLeakTest {
		return leakResult{}, errors.New("this is not a leak-test build")
	}
	if !manifest.State.Terminal() {
		return leakResult{}, errors.New("the leak test is still running")
	}
	var source *build.SourceRef
	var staged build.FileRecord
	for _, input := range manifest.Inputs {
		if input.Name == "source_map" {
			source, staged = input.Source, input
			break
		}
	}
	// A build that compiled a map of another game than its pipeline's is not
	// evidence about anything: the wrong compiler read it.
	if staged.Conversion != nil && staged.Conversion.Game != "" && staged.Conversion.Game != adapter.Profile {
		return leakResult{}, fmt.Errorf("this build ran the %s leak test on a %s map; its output is not a result",
			adapter.Profile, staged.Conversion.Game)
	}
	if source == nil || source.AssetType != aub.AssetTypeMap || source.AssetID == "" ||
		source.RevisionID == "" || source.Revision < 1 || len(source.ContentSHA256) != 64 || !source.Refetchable {
		return leakResult{}, errors.New("the build has no pinned AUB map source")
	}
	result := leakResult{SchemaVersion: adapter.ResultSchema, BuildID: manifest.BuildID,
		MapID: source.AssetID, RevisionID: source.RevisionID, Revision: source.Revision, ContentSHA256: source.ContentSHA256,
		BuildState: string(manifest.State)}
	for _, tool := range manifest.Tools {
		if tool.ToolVersion != "" {
			result.CompilerVersion = tool.ToolVersion
			break
		}
	}
	stepState := ""
	for _, step := range manifest.Steps {
		if step.ID == binding.CompileStep {
			result.CompileExitCode, stepState = step.ExitCode, string(step.State)
			if step.Skipped {
				stepState = ""
			}
			break
		}
	}
	var bsp *bool
	for _, output := range manifest.Outputs {
		var limit int64
		switch output.Name {
		case binding.Pointfile:
			limit = leakadapter.MaxPointfileBytes
		case binding.Log:
			limit = 16 << 20
		case binding.BSP:
			// Only whether this run left one. Its bytes never leave this
			// machine in a leak result.
			present := !output.Missing && output.Path != ""
			bsp = &present
			continue
		default:
			continue
		}
		if output.Missing || output.Path == "" {
			continue
		}
		// The file is this build's own: it sits inside this build's output
		// directory, it still has the digest the build recorded when it
		// collected it from the job's fresh workspace, and — for a compiler
		// that names its point file after its input — it is named after the
		// source this build staged. Nothing is ever looked for by pattern.
		if output.Name == binding.Pointfile && adapter.PointfileBesideSource && !sameStem(output.Path, staged.Path) {
			return leakResult{}, errors.New("the recorded point file is not named after this build's map source")
		}
		content, err := readLeakOutput(dir, manifest.BuildID, output, limit)
		if err != nil {
			return leakResult{}, err
		}
		if output.Name == binding.Pointfile {
			result.Pointfile, result.PointfileSHA256 = content, strings.TrimPrefix(output.SHA256, "sha256:")
		}
		if output.Name == binding.Log {
			result.Log, result.LogSHA256 = content, strings.TrimPrefix(output.SHA256, "sha256:")
		}
	}
	if result.Log == "" {
		return leakResult{}, errNoLeakLog
	}
	if adapter.ResultSchema == leakadapter.Schema10 {
		return result, nil
	}
	result.GameProfile, result.Compiler = adapter.Profile, adapter.Compiler
	result.PointfileFormat, result.PointfileDirection = adapter.PointfileFormat, adapter.Direction
	if len(staged.SHA256) == 64+len("sha256:") {
		result.CompilerSourceSHA256 = strings.TrimPrefix(staged.SHA256, "sha256:")
	}
	if adapter.Classify != nil {
		verdict := adapter.Classify(leakadapter.Evidence{Log: result.Log, CompilerVersion: result.CompilerVersion,
			ExitCode: result.CompileExitCode, StepState: stepState, Pointfile: result.Pointfile, BSP: bsp})
		result.Diagnostic = &verdict
	}
	return result, nil
}

// sameStem reports whether two paths name the same file apart from its
// extension. A staged source with no recorded path cannot be compared, and is
// not a reason to refuse.
func sameStem(output, source string) bool {
	if source == "" {
		return true
	}
	stem := func(path string) string {
		base := filepath.Base(path)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return stem(output) == stem(source)
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

// leakTestView is what the Build page needs to know about a leak-test build
// without knowing any pipeline by name: which game and compiler it is, which
// output is the route, and — when this program reads the run itself — what the
// run says. Nil for a build that is not a leak test.
func (s *Server) leakTestView(manifest *build.Manifest) map[string]any {
	adapter, binding, ok := leakBindingFor(manifest)
	if !ok {
		return nil
	}
	view := map[string]any{
		"game_profile": adapter.Profile, "compiler": adapter.Compiler, "compile_step": binding.CompileStep,
		"pointfile_output": binding.Pointfile, "pointfile_format": adapter.PointfileFormat,
		"pointfile_direction": adapter.Direction,
	}
	if adapter.Classify == nil || !manifest.State.Terminal() {
		return view
	}
	result, err := s.buildLeakResult(manifest.BuildID)
	if err != nil {
		view["unreadable"] = err.Error()
		return view
	}
	view["diagnostic"] = result.Diagnostic
	return view
}
