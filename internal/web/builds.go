package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetref"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetsync"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3packages"
)

// The Build area: several supervised jobs, wired, with a manifest.
//
// # A build outlives its HTTP request, and its record outlives the process
//
// A compile takes minutes. Nothing here holds a request open for one: the POST
// returns as soon as the build has an identity, and everything after that is
// read from the manifest on disk. That is not an optimisation, it is what makes
// the page reloadable — a user who closes the tab, or restarts the Companion,
// comes back to a build list that is exactly as complete as it was, because the
// manifest was never in memory to lose.
//
// [buildRuns] holds only what genuinely cannot be on disk: the log stream while
// it is being produced, and the fact that THIS process is the one running it.
//
// # Cancelling stops the step, not the bookkeeping
//
// A build is cancelled by cancelling the job its current step is, through the
// same [job.Service] a `companion job cancel` uses — so the process tree is
// signalled the way the executor signals one, the step records that it was
// cancelled, and the manifest is completed rather than abandoned. Cancelling the
// build's context instead would leave a compiler running with nothing waiting
// for it.

// buildRuns is what this process knows about builds it is running.
type buildRuns struct {
	mu   sync.Mutex
	runs map[string]*buildRun
}

func newBuildRuns() *buildRuns { return &buildRuns{runs: map[string]*buildRun{}} }

// buildRun is one build this process started.
type buildRun struct {
	// ID is empty until the runner has created the build directory. See
	// [build.Options.Announce].
	ID        string
	Label     string
	Pipeline  string
	StartedAt time.Time

	// log is the bounded live output. Bounded because a compiler can emit
	// hundreds of megabytes and a page that was streaming one must not become
	// the reason the machine runs out of memory.
	log *tailBuffer

	// cancel stops the run's context. It is for shutdown; cancelling a build
	// the user asked to stop goes through the executor. See the file comment.
	cancel context.CancelFunc

	mu       sync.Mutex
	done     bool
	err      string
	manifest *build.Manifest
}

func (r *buildRuns) put(run *buildRun) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[run.ID] = run
}

func (r *buildRuns) get(id string) (*buildRun, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	return run, ok
}

// active is every build this process is still running, newest first.
func (r *buildRuns) active() []*buildRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*buildRun, 0, len(r.runs))
	for _, run := range r.runs {
		run.mu.Lock()
		finished := run.done
		run.mu.Unlock()
		if !finished {
			out = append(out, run)
		}
	}
	return out
}

func (r *buildRun) finish(manifest *build.Manifest, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.done, r.manifest = true, manifest
	if err != nil {
		r.err = err.Error()
	}
}

func (r *buildRun) state() (finished bool, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.done, r.err
}

// tailBuffer keeps the last bytes of a stream and counts what fell off.
//
// The last, not the first: a build's live view is being watched by somebody
// waiting for it to finish, and what they are waiting to see is the end. The
// whole output is not lost — it is in each step's own job log, which is
// captured with a head and a tail by internal/job and is what the manifest
// points at.
type tailBuffer struct {
	mu      sync.Mutex
	data    []byte
	dropped int64
	limit   int
}

func newTailBuffer(limit int) *tailBuffer { return &tailBuffer{limit: limit} }

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if extra := len(b.data) - b.limit; extra > 0 {
		b.data = b.data[extra:]
		b.dropped += int64(extra)
	}
	return len(p), nil
}

func (b *tailBuffer) String() (string, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data), b.dropped
}

// liveLogBytes is how much of a running build's output the page can see.
const liveLogBytes = 128 << 10

func (s *Server) buildAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/build/pipelines":               s.handleBuildPipelines,
		"POST /api/v1/build/preview":                s.handleBuildPreview,
		"GET /api/v1/build/runs":                    s.handleBuildList,
		"POST /api/v1/build/runs":                   s.handleBuildStart,
		"GET /api/v1/build/runs/{id}":               s.handleBuildGet,
		"POST /api/v1/build/runs/{id}/cancel":       s.handleBuildCancel,
		"GET /api/v1/build/runs/{id}/output/{name}": s.handleBuildOutput,
	}
}

// buildRunner builds a [build.Runner] against this run's directories.
func (s *Server) buildRunner(announce func(*build.Manifest)) (*build.Runner, error) {
	service, err := s.requireJobsService()
	if err != nil {
		return nil, err
	}
	dir, err := s.buildsDir()
	if err != nil {
		return nil, err
	}
	bindingsPath, err := s.bindingsPath()
	if err != nil {
		return nil, err
	}
	return build.New(build.Options{
		Service:   service,
		Dir:       dir,
		Bindings:  binding.Lookup(bindingsPath),
		Companion: s.version,
		Announce:  announce,
	})
}

// reconcileBuild records a build a stopped Companion left `running`; see
// build.Reconcile. Only for a build this process is not running itself.
func (s *Server) reconcileBuild(manifest *build.Manifest) {
	if s.jobs == nil || manifest.Directory == "" {
		return
	}
	if build.Reconcile(manifest, s.jobs.Get, time.Now()) {
		// A manifest that cannot be rewritten is still answered reconciled;
		// the next read tries the write again.
		_ = manifest.Save(manifest.Directory)
	}
}

func (s *Server) requireJobsService() (*job.Service, error) {
	if s.jobs == nil {
		return nil, errors.New("this build has no job service")
	}
	return s.jobs, nil
}

// handleBuildPipelines lists what can be built, and says what is missing when
// something cannot be.
//
// `runnable` is not a guess: it is the same capability index the runner itself
// resolves with, so a pipeline listed as ready is one whose every step has a
// provider on this machine.
func (s *Server) handleBuildPipelines(w http.ResponseWriter, r *http.Request) {
	runner, err := s.buildRunner(nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
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

	items := make([]map[string]any, 0)
	for _, entry := range entries {
		pipeline, isPipeline := entry.Profile.(*profile.PipelineProfile)
		if !isPipeline {
			continue
		}
		meta := entry.Profile.Metadata()
		steps := make([]map[string]any, 0, len(pipeline.Steps))
		missing := make([]string, 0)
		runnable := true
		for _, step := range pipeline.Steps {
			row := map[string]any{
				"id": step.ID, "title": step.Title, "capability": step.Capability,
			}
			tool, action, provided := runner.Resolver().ProviderForStep(step)
			if provided {
				toolMeta := tool.Metadata()
				row["provider"] = map[string]any{
					"id": toolMeta.ID, "name": toolMeta.Name,
					"version": toolMeta.Version, "action": action.ID,
				}
				if toolEntry, ok := runner.Resolver().EntryForStep(step); ok {
					local, _ := set.Find(toolMeta.ID)
					row["provider_trust"] = toolEntry.Trust
					row["provider_authorized"] = profile.Authorize(
						toolEntry.Profile, toolEntry.Trust, toolEntry.Digest, local.Grant) == nil
					row["provider_version"] = local.ResolvedVersion
					row["provider_bound"] = local.ProfileID != ""
				}
			} else {
				missing = append(missing, step.Capability)
				runnable = false
			}
			steps = append(steps, row)
		}
		family := documentFamily(pipeline)
		items = append(items, map[string]any{
			"id": meta.ID, "name": meta.Name, "summary": meta.Summary,
			"version": meta.Version, "trust": entry.Trust, "digest": entry.Digest,
			"inputs": inputPayload(pipeline.Inputs, family), "outputs": pipeline.Outputs,
			"steps": steps, "missing_capabilities": missing, "runnable": runnable,
			"engine_family": family, "maturity": describeMaturity(family),
			"readiness": pipelineReadiness(pipeline, runner.Resolver(), set).view(),
		})
		// A leak test is a question about a map, not a build of it. The page
		// asks this flag rather than knowing a pipeline id (`Q3_018`).
		if s.isLeakPipeline(meta.ID) {
			items[len(items)-1]["leak_test"] = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// inputPayload is a pipeline's declared inputs, each carrying the source kind
// the Build page should ask for.
//
// The classification is done HERE and not in the browser: which artifact role is
// a map source, and which representation a given engine family uses for its
// textures, is schema knowledge, and a second copy of it in JavaScript is a copy
// that would eventually disagree. The page renders what this says
// (AUCOM/AUT 246I). Every existing field is passed through unchanged, so this is
// an additive payload change and an older page ignores `source_kind`.
func inputPayload(inputs []profile.InputSpec, family string) []map[string]any {
	rows := make([]map[string]any, 0, len(inputs))
	for _, input := range inputs {
		rows = append(rows, map[string]any{
			"name":        input.Name,
			"title":       input.Title,
			"role":        input.Role,
			"required":    input.Required,
			"extensions":  input.Extensions,
			"stage_with":  input.StageWith,
			"source_kind": profile.InputSourceKind(input.Role, family),
		})
	}
	return rows
}

// buildRequestBody is what the Build area sends.
type buildRequestBody struct {
	Pipeline string `json:"pipeline"`
	// Inputs maps a declared pipeline input to a path on this machine, or to an
	// `aub:<type>/<id>@<revision>` reference — see internal/assetref. Each one
	// is a FILE.
	Inputs map[string]string `json:"inputs,omitempty"`
	// Roots maps a root role to a DIRECTORY on this machine, for this build
	// only — `content_root` is the texture folder EricW's `qbsp` is given with
	// `-wadpath`. Additive: a page that sends none behaves exactly as before.
	//
	// `AUCOM/AUT 246I` classified the Quake 1 texture input as a folder in the
	// page and in internal/profile and left this request model alone, so the
	// folder the user chose went through `checkOpenFile` and was rejected as
	// "not a file". This field is the other half of that fix.
	Roots         map[string]string            `json:"roots,omitempty"`
	Options       map[string]map[string]string `json:"options,omitempty"`
	Label         string                       `json:"label,omitempty"`
	Strict        bool                         `json:"strict,omitempty"`
	LeakRequestID string                       `json:"leak_request_id,omitempty"`
}

// resolveRoots checks each supplied root as a DIRECTORY.
//
// Checked here, by name, for the reason the inputs are: a message that says
// which root was wrong is one the user can act on, and the alternative is a
// failure from inside the build naming a path and not a field.
//
// What it does NOT do is invent provenance. A directory a user chose is a
// directory a user chose; the Build & Run coordinator, which holds a verified
// bundle, records the bundle itself.
func (s *Server) resolveRoots(request buildRequestBody) (
	map[string]string, map[string]build.RootSource, error) {
	if len(request.Roots) == 0 {
		return nil, nil, nil
	}
	roots := make(map[string]string, len(request.Roots))
	sources := make(map[string]build.RootSource, len(request.Roots))
	for role, value := range request.Roots {
		path, err := checkDirectory(value)
		if err != nil {
			return nil, nil, fmt.Errorf("the %q folder: %w", role, err)
		}
		roots[role] = path
		sources[role] = build.RootSource{Kind: build.RootFromLocalDirectory}
	}

	return roots, sources, nil
}

// resolveInputs turns the request's inputs into files, fetching from AUB where
// an input names an asset.
//
// The staging directory is inside the builds directory and per-request, so two
// builds started at once cannot write over each other's copy of a map.
func (s *Server) resolveInputs(ctx context.Context, request buildRequestBody) (
	map[string]string, map[string]build.SourceRef, map[string]build.Conversion, *build.BoundPackages, error) {
	anyRef := false
	for _, value := range request.Inputs {
		if assetref.Is(value) {
			anyRef = true
			break
		}
	}
	resolved := make(map[string]string, len(request.Inputs))
	var sources map[string]build.SourceRef
	if !anyRef {
		// Local files only. Each is checked here rather than left to fail
		// somewhere inside the build, so the message names the input.
		for name, value := range request.Inputs {
			path, err := checkOpenFile(value)
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("the input %q: %w", name, err)
			}
			resolved[name] = path
		}
		if !assetref.HasLocalAPMap(resolved) {
			return resolved, nil, nil, nil, nil
		}
	}

	buildsDir, err := s.buildsDir()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stage, err := os.MkdirTemp(ensureDir(buildsDir), "inputs-")
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("creating a staging directory: %w", err)
	}
	local := resolved
	if anyRef {
		store, err := s.assets()
		if err != nil {
			return nil, nil, nil, nil, err
		}
		// The syncer is optional on purpose: a pinned revision already in the cache
		// builds with no session and no network. Only `current`, and a revision
		// this machine has never fetched, need one.
		var syncer *assetsync.Syncer
		if client := s.aubClient(); client != nil && client.Authenticated() {
			if made, err := assetsync.NewSyncer(ctx, client, store); err == nil {
				syncer = made
			}
		}
		resolved, sources, err = assetref.ResolveAll(ctx, store, syncer, request.Inputs, stage)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		local = request.Inputs
	}
	// A local `.apmap` is converted from a copy in the stage, never beside the
	// user's own file. Before Q3_010 only the terminal did this; the page sent
	// the APMap to the compiler as it was.
	resolved, err = assetref.StageLocalAPMaps(local, resolved, stage)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// The packages the saved map is bound to, by digest, through the Companion's
	// own session: the page never sees the token or a download address.
	store, err := s.assets()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var account q3packages.Account
	if client := s.aubClient(); client != nil && client.Authenticated() {
		account = client
	}
	packages, err := assetref.BoundPackages(ctx, account, store, resolved)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	converted, sources, conversions, err := assetref.ConvertAPMapInputsRecorded(ctx, s.runner, resolved, sources)
	return converted, sources, conversions, packages, err
}

func ensureDir(dir string) string {
	// Best effort: a failure here surfaces as the MkdirTemp error immediately
	// below, which names the directory and the reason.
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

// handleBuildPreview resolves every stage and starts nothing.
//
// It is the command preview: which tool provides each step, at which version,
// with which argv. Everything that can be checked without running is checked
// here, so a pipeline that would fail on its third step fails before the user
// has waited through two.
func (s *Server) handleBuildPreview(w http.ResponseWriter, r *http.Request) {
	var request buildRequestBody
	if !decodeJSON(w, r, &request) {
		return
	}
	runner, err := s.buildRunner(nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	inputs, sources, conversions, packages, err := s.resolveInputs(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	roots, rootSources, err := s.resolveRoots(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	manifest, err := runner.Preview(build.Request{
		PipelineID:  request.Pipeline,
		Inputs:      inputs,
		Sources:     sources,
		Conversions: conversions,
		Packages:    packages,
		Roots:       roots,
		RootSources: rootSources,
		Options:     request.Options,
		Label:       request.Label,
		Strict:      request.Strict,
	})
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, manifest)
}

// handleBuildStart begins a build and returns as soon as it has an identity.
//
// 202 with the build id: everything after this is read back from
// /api/v1/build/runs/{id}, which reads the manifest off the disk. A response
// that waited for the build would be a response that timed out, and a page that
// only knew what it had been told in that response would be a page that
// forgets everything on reload.
func (s *Server) handleBuildStart(w http.ResponseWriter, r *http.Request) {
	var request buildRequestBody
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Pipeline == "" {
		writeError(w, http.StatusBadRequest, errors.New("a build needs a pipeline id"))
		return
	}
	// The readiness the pipeline selectors show is the one enforced here: a
	// crafted or stale request for a pipeline whose setup is incomplete starts
	// nothing and leaves no failed build behind (NEW_310).
	if unready, ok := s.unreadyPipeline(request.Pipeline); ok {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     "the pipeline " + request.Pipeline + " needs setup before it can build",
			"class":     "pipeline_not_ready",
			"readiness": unready.view(),
		})
		return
	}

	// The inputs are resolved on the request's own context, before anything is
	// started: fetching a map from AUB is the part that can fail for reasons
	// the user must be told about immediately, and a build that had already
	// started would report them as a build failure instead.
	inputs, sources, conversions, packages, err := s.resolveInputs(r.Context(), request)
	if err != nil {
		writeError(w, aubStatus(err), err)
		return
	}
	roots, rootSources, err := s.resolveRoots(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	run := &buildRun{
		Label:     request.Label,
		Pipeline:  request.Pipeline,
		StartedAt: time.Now().UTC(),
		log:       newTailBuffer(liveLogBytes),
	}
	// Not the request's context: the build must survive the response. Cancelled
	// on shutdown, which is what Close is for.
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel

	identified := make(chan string, 1)
	// Set below, once the editor's request is taken: a leak build tells the
	// editor which step is running (leakrelay.go).
	var leakRequest *aub.LeakTestLink
	var leakBinding *leakadapter.Binding
	announce := func(manifest *build.Manifest) {
		if leakRequest != nil {
			s.reportLeakBuild(*leakRequest, manifest)
		}
		if run.ID != "" || manifest.BuildID == "" {
			return
		}
		run.ID = manifest.BuildID
		s.builds.put(run)
		identified <- manifest.BuildID
	}

	runner, err := s.buildRunner(announce)
	if err != nil {
		cancel()
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if request.LeakRequestID != "" {
		if !s.matchesPendingLeakRequest(request.LeakRequestID, sources["source_map"]) {
			cancel()
			writeError(w, http.StatusConflict, errors.New("the editor leak request expired or no longer matches this pinned map revision"))
			return
		}
		// The same resolver the review used, on the bytes that were just
		// staged: the game the pinned APMap declares decides the pipeline. A
		// request that names another one — a stale page, a hand-made call —
		// starts nothing.
		conversion, converted := conversions["source_map"]
		binding, err := s.leakBindingForBuild(request.Pipeline, conversion, converted)
		if err != nil {
			cancel()
			writeError(w, http.StatusConflict, err)
			return
		}
		leakBinding = &binding
		configDir, err := s.configDir()
		if err != nil {
			cancel()
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		consumed, err := leakintent.Consume(leakintent.Path(configDir), request.LeakRequestID, time.Now().UTC())
		if err != nil || consumed == nil {
			cancel()
			writeError(w, http.StatusConflict, errors.New("the editor leak request was already used"))
			return
		}
		leakRequest = consumed
	}

	failed := make(chan error, 1)
	go func() {
		defer cancel()
		manifest, err := runner.Run(ctx, build.Request{
			PipelineID:  request.Pipeline,
			Inputs:      inputs,
			Sources:     sources,
			Conversions: conversions,
			Packages:    packages,
			Roots:       roots,
			RootSources: rootSources,
			Options:     request.Options,
			Label:       request.Label,
			LeakTest:    leakBinding,
			Strict:      request.Strict,
			Mirror:      run.log,
		})
		run.finish(manifest, err)
		if leakRequest != nil && manifest != nil && manifest.BuildID != "" && manifest.State.Terminal() {
			// The delivery is recorded beside the build, so the page can show
			// it apart from the compiler's verdict and send it again.
			if returned := s.deliverLeakResult(manifest.BuildID, *leakRequest); returned.State != "returned" {
				_, _ = run.log.Write([]byte("\nCompanion could not return the leak result to the editor: " + returned.Error + "\n"))
			}
		}
		if run.ID == "" {
			// It failed before it had a directory — an unresolvable pipeline,
			// a capability nothing provides. There is no manifest to point the
			// caller at, so the error is the whole answer.
			if err == nil {
				err = errors.New("the build ended without producing a manifest")
			}
			failed <- err
		}
	}()

	select {
	case id := <-identified:
		w.Header().Set("Location", "/api/v1/build/runs/"+id)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"build": id, "pipeline": request.Pipeline, "label": request.Label, "started": true,
		})
	case err := <-failed:
		writeError(w, jobStatus(err), err)
	case <-r.Context().Done():
		// The caller went away before the build was named. The build itself
		// carries on — it is already staging files — and will appear in the
		// list, because the list is read from disk.
		writeError(w, http.StatusRequestTimeout, r.Context().Err())
	}
}

func (s *Server) handleBuildList(w http.ResponseWriter, r *http.Request) {
	dir, err := s.buildsDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	manifests, err := build.List(dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("limit=%q is not a count", raw))
			return
		}
		if limit < len(manifests) {
			manifests = manifests[:limit]
		}
	}
	// Which of these this process is still running. A manifest left in
	// `running` by a previous run of the Companion is NOT running, and a page
	// that could not tell the difference would offer a Cancel button for a
	// build nothing is doing.
	live := map[string]bool{}
	for _, run := range s.builds.active() {
		live[run.ID] = true
	}
	items := make([]map[string]any, 0, len(manifests))
	for _, manifest := range manifests {
		if !live[manifest.BuildID] {
			s.reconcileBuild(manifest)
		}
		items = append(items, map[string]any{"manifest": manifest, "live": live[manifest.BuildID]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleBuildGet(w http.ResponseWriter, r *http.Request) {
	dir, err := s.buildsDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	id := r.PathValue("id")
	manifest, err := build.Find(dir, id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if _, running := s.builds.get(id); !running {
		s.reconcileBuild(manifest)
	}
	body := map[string]any{"manifest": manifest, "live": false}
	// The statement about the family this build was for, so the progress panel
	// can carry it and offer the report without a second request. Empty for a
	// family this build makes no claim about, and the page draws nothing then.
	body["maturity"] = describeMaturity(manifest.EngineFamily)
	body["maturity_message"] = maturity.Of(manifest.EngineFamily).Message
	if leak := s.leakTestView(manifest); leak != nil {
		body["leak_test"] = leak
	}
	if run, running := s.builds.get(id); running {
		finished, message := run.state()
		body["live"] = !finished
		if message != "" {
			body["error"] = message
		}
		text, dropped := run.log.String()
		body["log"] = text
		body["log_dropped"] = dropped
	}
	writeJSON(w, http.StatusOK, body)
}

// handleBuildCancel stops the step that is running.
//
// Through the executor, by job id, so the process tree gets the same SIGTERM
// then SIGKILL any other cancelled job gets and the step records what happened.
// A build that this process is not running cannot be cancelled from here, and
// says so rather than pretending.
func (s *Server) handleBuildCancel(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireJobs(w)
	if !ok {
		return
	}
	dir, err := s.buildsDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	id := r.PathValue("id")
	manifest, err := build.Find(dir, id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	run, running := s.builds.get(id)
	if !running {
		writeError(w, http.StatusConflict, fmt.Errorf(
			"build %s is not running in this Companion; nothing here can stop it", id))
		return
	}
	if finished, _ := run.state(); finished {
		writeError(w, http.StatusConflict, fmt.Errorf("build %s has already finished", id))
		return
	}

	cancelled := make([]string, 0, 1)
	for _, step := range manifest.Steps {
		if step.JobID == "" || step.State != job.Running {
			continue
		}
		if _, err := service.Cancel(step.JobID); err != nil {
			writeError(w, jobStatus(err), fmt.Errorf("stopping the %s step: %w", step.ID, err))
			return
		}
		cancelled = append(cancelled, step.JobID)
	}
	if len(cancelled) == 0 {
		// Between two steps: nothing is running yet, so there is no process to
		// signal. The run's own context is what stops it going further.
		run.cancel()
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"build": id, "cancelled_jobs": cancelled})
}

// handleBuildOutput serves one file a build published.
//
// Addressed by the pipeline's DECLARED OUTPUT NAME, not by a file name. The
// manifest records what each output is and where it landed, so the name in the
// URL selects a record and the record supplies the path — which means the path
// never has to be built out of anything a caller sent.
//
// The recorded path is then checked to be inside this build's own `output/`
// directory before it is opened. That check is not redundant with the first
// one: a manifest is a file on disk, and a file on disk is not something this
// handler gets to assume nothing has edited.
func (s *Server) handleBuildOutput(w http.ResponseWriter, r *http.Request) {
	dir, err := s.buildsDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	id, name := r.PathValue("id"), r.PathValue("name")
	manifest, err := build.Find(dir, id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	var record *build.FileRecord
	offered := make([]string, 0, len(manifest.Outputs))
	for index, output := range manifest.Outputs {
		offered = append(offered, output.Name)
		if output.Name == name {
			record = &manifest.Outputs[index]
		}
	}
	switch {
	case record == nil:
		writeError(w, http.StatusNotFound, fmt.Errorf(
			"build %s declares no output called %q; it declares %v", id, name, offered))
		return
	case record.Missing || record.Path == "":
		// A declared output that was not produced is a fact the manifest
		// records, not a missing file: an optional artifact that did not apply
		// and a required one that failed are both this, and the manifest says
		// which.
		writeError(w, http.StatusNotFound, fmt.Errorf(
			"build %s declared %q and did not produce it", id, name))
		return
	}

	root := filepath.Join(dir, id, "output")
	path := filepath.Clean(record.Path)
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		writeError(w, http.StatusForbidden, fmt.Errorf(
			"the manifest points %q at %s, which is not inside this build's output directory",
			name, path))
		return
	}

	file, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Never sniffed and never rendered: a build output is a tool's output, and
	// a browser that decided one was HTML would run it as a page on this origin.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(filepath.Base(path)))
	http.ServeContent(w, r, "", info.ModTime(), file)
}

// Server.Close, which stops every build this process is running, is in
// lifecycle_api.go beside the rest of what a stopping Companion does.

// unreadyPipeline answers whether an installed pipeline cannot run here yet,
// with its readiness. An id that names no pipeline is left to the runner,
// which already refuses it with its own error.
func (s *Server) unreadyPipeline(id string) (readiness, bool) {
	runner, err := s.buildRunner(nil)
	if err != nil {
		return readiness{}, false
	}
	catalog, err := s.catalog()
	if err != nil {
		return readiness{}, false
	}
	entries, err := catalog.List()
	if err != nil {
		return readiness{}, false
	}
	set, _, err := s.bindings()
	if err != nil {
		return readiness{}, false
	}
	for _, entry := range entries {
		pipeline, isPipeline := entry.Profile.(*profile.PipelineProfile)
		if !isPipeline || entry.Profile.Metadata().ID != id {
			continue
		}
		state := pipelineReadiness(pipeline, runner.Resolver(), set)
		return state, !state.Ready
	}
	return readiness{}, false
}
