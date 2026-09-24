package joinready_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetsync"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/joincontent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/joinready"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

type remote struct {
	detail    aub.HostedGameDetail
	detailErr error
	profile   aub.GameProfileSummary
}

func (r *remote) HostedGameByID(context.Context, string) (aub.HostedGameDetail, error) {
	return r.detail, r.detailErr
}

func (r *remote) GameProfileBySlug(context.Context, string) (aub.GameProfileSummary, error) {
	if r.profile.Slug == "" {
		return aub.GameProfileSummary{}, aub.ErrGameProfileNotFound
	}

	return r.profile, nil
}

func (r *remote) GameJoinContent(context.Context, string) (aub.GameJoinContent, error) {
	return aub.GameJoinContent{}, &aub.APIError{StatusCode: http.StatusNotFound}
}

type previewer struct{ refuse string }

func (p previewer) Preview(request job.Request) (*job.Job, error) {
	return &job.Job{Request: request, Error: p.refuse, Command: &job.CommandPreview{Shell: "engine"}}, nil
}

type catalog []job.CatalogEntry

func (c catalog) List() ([]job.CatalogEntry, error) { return c, nil }
func (c catalog) Lookup(string) (job.CatalogEntry, error) {
	return job.CatalogEntry{}, job.ErrNoProfile
}

type fixture struct {
	remote  *remote
	local   joinready.Local
	set     *binding.Set
	entry   job.CatalogEntry
	program string
	owned   string
}

func newFixture(t *testing.T, trust profile.Trust) *fixture {
	t.Helper()
	document, err := profile.DecodeEngine(enginefixture.ProfileJSON)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := profile.Digest(document)
	f := &fixture{set: binding.NewSet(), entry: job.CatalogEntry{Profile: document, Trust: trust, Digest: digest}}
	f.owned = t.TempDir()
	os.MkdirAll(filepath.Join(f.owned, "id1"), 0o755)
	os.WriteFile(filepath.Join(f.owned, "id1", "pak0.pak"), []byte("PACK"), 0o644)
	f.program = filepath.Join(t.TempDir(), "engine")
	os.WriteFile(f.program, []byte("x"), 0o755)
	store, _ := assetsync.Open(t.TempDir())
	f.local = joinready.Local{Catalog: catalog{f.entry}, Bindings: func() (*binding.Set, error) { return f.set, nil },
		Checker: engine.Checker{Platform: profile.Platform{OS: "linux", Arch: "amd64"}},
		Stager:  &joincontent.Stager{Root: filepath.Join(t.TempDir(), "join-content"), Store: store},
		Runner:  previewer{}}
	f.remote = &remote{detail: aub.HostedGameDetail{Game: aub.HostedGame{ID: "g1", Title: "Chapel", State: "live",
		Joinable: true, GameFamily: "quake1", EngineRuntime: "aucom-fixture", Endpoint: "203.0.113.4:26000",
		JoinContent: aub.JoinContent{State: aub.JoinContentNotRequired, ContentIdentity: "quake1:id1"}}}}

	return f
}

func (f *fixture) bind(t *testing.T, mutate func(*binding.LocalBinding)) {
	t.Helper()
	document := f.entry.Profile
	local := binding.LocalBinding{SchemaVersion: binding.SchemaVersion, ProfileID: document.Metadata().ID,
		ProfileVersion: document.Metadata().Version, ProfileDigest: f.entry.Digest, Trust: f.entry.Trust,
		Acquisition: profile.AcquireUserPath, Executables: map[string]string{"engine": f.program},
		Roots: map[string]string{"game_root": f.owned, "content_root": t.TempDir()}, UpdatedAt: time.Now()}
	if f.entry.Trust != profile.TrustBuiltin {
		local.Grant = profile.NewGrant(document, f.entry.Trust, f.entry.Digest, time.Now())
	}
	if mutate != nil {
		mutate(&local)
	}
	if err := f.set.Put(local); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) assess(t *testing.T) joinready.Report {
	t.Helper()
	report, err := joinready.Assess(context.Background(), f.remote, f.local, "g1")
	if err != nil {
		t.Fatal(err)
	}

	return report
}

func stepState(report joinready.Report, id string) joinready.Step {
	for _, step := range report.Steps {
		if step.ID == id {
			return step
		}
	}

	return joinready.Step{}
}

func TestReadyIsOnlyReportedAfterTheServicePreviewedTheCommand(t *testing.T) {
	f := newFixture(t, profile.TrustBuiltin)
	f.bind(t, nil)
	if report := f.assess(t); report.State != joinready.StateReadyForReview || report.Preview == nil {
		t.Fatalf("report = %s %+v", report.State, report.Steps)
	}
	f.local.Runner = previewer{refuse: "the executor refused"}
	report := f.assess(t)
	if report.State == joinready.StateReadyForReview || stepState(report, joinready.StepCommand).State != joinready.CommandPreviewFailed {
		t.Fatalf("a failed preview reads as %s", report.State)
	}
}

func TestEachLocalGapIsItsOwnNamedState(t *testing.T) {
	for name, tc := range map[string]struct {
		trust  profile.Trust
		mutate func(*fixture, *binding.LocalBinding)
		skip   bool
		step   string
		state  string
		action string
	}{
		"community document never reviewed": {trust: profile.TrustCommunity, skip: true,
			step: joinready.StepEngineProfile, state: joinready.EngineUnreviewed, action: joinready.ActionApproveProfile},
		"approval withdrawn": {trust: profile.TrustCommunity, mutate: func(_ *fixture, b *binding.LocalBinding) { b.Grant = nil },
			step: joinready.StepEngineProfile, state: joinready.EngineUnapproved, action: joinready.ActionApproveProfile},
		"document changed since approval": {trust: profile.TrustCommunity, mutate: func(_ *fixture, b *binding.LocalBinding) {
			b.ProfileDigest = "sha256:" + strings.Repeat("0", 64)
			b.Grant = nil
		}, step: joinready.StepEngineProfile, state: joinready.EngineChanged, action: joinready.ActionApproveProfile},
		"no program chosen": {trust: profile.TrustBuiltin, mutate: func(_ *fixture, b *binding.LocalBinding) { b.Executables = nil },
			step: joinready.StepEngineProgram, state: joinready.LocalMissing, action: joinready.ActionChooseProgram},
		"program moved": {trust: profile.TrustBuiltin, mutate: func(f *fixture, b *binding.LocalBinding) {
			b.Executables = map[string]string{"engine": filepath.Join(f.owned, "nowhere", "engine")}
		}, step: joinready.StepEngineProgram, state: joinready.LocalStale, action: joinready.ActionChooseProgram},
		"no game folder": {trust: profile.TrustBuiltin, mutate: func(_ *fixture, b *binding.LocalBinding) {
			delete(b.Roots, "game_root")
		}, step: joinready.StepGameContent, state: joinready.LocalMissing, action: joinready.ActionChooseGameRoot},
		"game folder emptied": {trust: profile.TrustBuiltin, mutate: func(f *fixture, _ *binding.LocalBinding) {
			os.RemoveAll(filepath.Join(f.owned, "id1"))
		}, step: joinready.StepGameContent, state: joinready.LocalStale, action: joinready.ActionChooseGameRoot},
	} {
		f := newFixture(t, tc.trust)
		if !tc.skip {
			f.bind(t, func(b *binding.LocalBinding) {
				if tc.mutate != nil {
					tc.mutate(f, b)
				}
			})
		}
		report := f.assess(t)
		step := stepState(report, tc.step)
		if step.State != tc.state || step.Action != tc.action || step.Done || report.State != joinready.StateSetupRequired {
			t.Errorf("%s: %s = %q action %q done %v, overall %s", name, tc.step, step.State, step.Action, step.Done, report.State)
		}
	}
}

func TestTheGameItselfDecidesBeforeAnythingLocal(t *testing.T) {
	f := newFixture(t, profile.TrustBuiltin)
	f.bind(t, nil)

	f.remote.detail.Game.State, f.remote.detail.Game.Joinable = "offline", false
	if report := f.assess(t); report.State != joinready.StateNotJoinable || report.Next != joinready.StepGame {
		t.Fatalf("ended = %s next %s", report.State, report.Next)
	}
	f.remote.detail.Game.State, f.remote.detail.Game.Joinable, f.remote.detail.Game.OwnedByMe = "live", false, true
	if step := stepState(f.assess(t), joinready.StepGame); step.State != joinready.GameOwn {
		t.Fatalf("own game = %q", step.State)
	}
	f.remote.detailErr = &aub.APIError{StatusCode: http.StatusUnauthorized}
	if report := f.assess(t); report.State != joinready.StateSignInRequired {
		t.Fatalf("revoked session = %s", report.State)
	}
	f.remote.detailErr = &aub.APIError{StatusCode: http.StatusNotFound}
	if report := f.assess(t); report.State != joinready.StateNotJoinable {
		t.Fatalf("gone = %s", report.State)
	}
	if report, _ := joinready.Assess(context.Background(), nil, f.local, "g1"); report.State != joinready.StateSignInRequired {
		t.Fatalf("no client = %s", report.State)
	}
}

func TestSilenceAboutContentIsAWarningNeverAVerification(t *testing.T) {
	f := newFixture(t, profile.TrustBuiltin)
	f.bind(t, nil)
	f.remote.detail.Game.JoinContent = aub.JoinContent{State: aub.JoinContentUndeclared}
	step := stepState(f.assess(t), joinready.StepJoinContent)
	if step.State != joinready.ContentUndeclared || step.Warning == "" || strings.Contains(strings.ToLower(step.Detail), "checked") {
		t.Fatalf("undeclared = %+v", step)
	}

	unreadable := false
	f.remote.detail.Game.JoinContent = aub.JoinContent{State: aub.JoinContentRequired,
		PackageSHA256: "sha256:" + strings.Repeat("a", 64), Readable: &unreadable}
	report := f.assess(t)
	if step = stepState(report, joinready.StepJoinContent); step.State != joinready.ContentUnreadable || step.Done {
		t.Fatalf("unreadable = %+v", step)
	}
	if report.State == joinready.StateReadyForReview {
		t.Fatal("an unreadable package reads as ready")
	}

	readable := true
	f.remote.detail.Game.JoinContent.Readable = &readable
	if step = stepState(f.assess(t), joinready.StepJoinContent); step.State != joinready.ContentUnavailable {
		t.Fatalf("a package AUB cannot serve = %+v", step)
	}
}

func TestAGameProfileThatContradictsTheGameBlocks(t *testing.T) {
	f := newFixture(t, profile.TrustBuiltin)
	f.bind(t, nil)
	f.remote.detail.Game.GameSlug = "quake1"
	f.remote.profile = aub.GameProfileSummary{Slug: "quake1", Name: "Quake", EngineFamily: "quake1"}
	if step := stepState(f.assess(t), joinready.StepGameProfile); step.State != joinready.ProfileResolved || !step.Done {
		t.Fatalf("resolved = %+v", step)
	}
	f.remote.profile = aub.GameProfileSummary{Slug: "quake1", Name: "Not Quake", EngineFamily: "quake3"}
	if step := stepState(f.assess(t), joinready.StepGameProfile); step.State != joinready.ProfileIncompatible || step.Done {
		t.Fatalf("incompatible = %+v", step)
	}
	f.remote.profile = aub.GameProfileSummary{}
	if step := stepState(f.assess(t), joinready.StepGameProfile); step.State != joinready.ProfileUnresolved || !step.Done || step.Warning == "" {
		t.Fatalf("unresolved = %+v", step)
	}
}

func TestTheHostsRuntimeNeverPicksAnExecutable(t *testing.T) {
	f := newFixture(t, profile.TrustBuiltin)
	f.bind(t, nil)
	f.remote.detail.Game.EngineRuntime = "somebody-elses-engine"
	f.remote.detail.Game.EngineProfileID = "listing9"
	report := f.assess(t)
	step := stepState(report, joinready.StepEngineProfile)
	if step.State != joinready.EngineMissing || step.Action != joinready.ActionInstallProfile || report.Request != nil {
		t.Fatalf("unknown runtime = %+v, request %v", step, report.Request)
	}
}
