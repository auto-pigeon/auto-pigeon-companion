package web

import (
	"context"
	"net/http"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// The page's side of the lifecycle: the lease, what is running, and Quit.

// quitCancelWait bounds how long a Quit that cancelled work waits for that work
// to stop before the process stops anyway. Past it, the shutdown path's own
// bounded cancellation (the executor's graceful-then-forced stop) takes over.
const quitCancelWait = 20 * time.Second

// hostedLeaseEndTimeout bounds telling AUB that this machine's hosted games
// ended. A shutdown must not hang on a server that is down; AUB's own expiry is
// what covers that case.
const hostedLeaseEndTimeout = 5 * time.Second

// codeWorkActive marks a Quit refused because something is running and the
// request did not say to cancel it. The page shows the list and asks.
const codeWorkActive = "work_active"

// leasePath is the page's lease route.
const leasePath = "/api/lifecycle/lease"

func (s *Server) lifecycleAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET " + leasePath:         s.handleLease,
		"GET /api/lifecycle":       s.handleLifecycle,
		"POST /api/lifecycle/quit": s.handleQuit,
	}
}

type lifecycleBody struct {
	Mode         string       `json:"mode"`
	GraceSeconds int          `json:"grace_seconds"`
	Leases       int          `json:"leases"`
	Active       []ActiveWork `json:"active"`
}

func (s *Server) handleLifecycle(w http.ResponseWriter, r *http.Request) {
	mode := "server"
	if s.lifecycle.Interactive() {
		mode = "interactive"
	}
	writeJSON(w, http.StatusOK, lifecycleBody{
		Mode:         mode,
		GraceSeconds: int(s.lifecycle.CloseGrace() / time.Second),
		Leases:       s.lifecycle.Leases(),
		Active:       s.ActiveWork(),
	})
}

// handleQuit is Quit Auto-Pigeon Companion. With nothing running it stops the
// process at once. With something running it refuses unless the request says
// to cancel it, so the page can show what would be cancelled and let the person
// keep the Companion running instead.
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CancelActive bool `json:"cancel_active"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	work := s.ActiveWork()
	if len(work) > 0 && !request.CancelActive {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  "Something is still running. Cancel it and quit, or keep the Companion running.",
			"code":   codeWorkActive,
			"active": work,
		})
		return
	}
	s.logf("quit: chosen in the page, %d running item(s) cancelled", len(work))
	if len(work) == 0 {
		writeJSON(w, http.StatusAccepted, map[string]any{"stopping": true, "cancelled": []ActiveWork{}})
		s.lifecycle.Stop(ExitQuit)
		return
	}
	s.cancelWork(work)
	writeJSON(w, http.StatusAccepted, map[string]any{"stopping": true, "cancelled": work})
	go func() {
		deadline := time.Now().Add(quitCancelWait)
		for len(s.ActiveWork()) > 0 && time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
		}
		s.lifecycle.Stop(ExitQuit)
	}()
}

// ActiveWork is everything this process is running that closing the page must
// not silently destroy: jobs (compiles, conversions, running games, hosted
// servers), builds started from the page, Build & Run sequences, and join
// downloads. Memory and this process's own few job records only — never the
// whole job store — because the lifecycle asks every second.
func (s *Server) ActiveWork() []ActiveWork {
	work := []ActiveWork{}
	if s.jobs != nil {
		for _, j := range s.jobs.Active() {
			kind := "job"
			switch {
			case j.Hosting():
				kind = "hosted_game"
			case j.SessionRole == profile.SessionClient:
				kind = "game"
			}
			label := j.ActionTitle
			if label == "" {
				label = j.ActionID
			}
			if j.ProfileID != "" {
				label += " — " + j.ProfileID
			}
			work = append(work, ActiveWork{Kind: kind, ID: j.ID, Label: label, State: string(j.State)})
		}
	}
	for _, run := range s.builds.active() {
		label := run.Label
		if label == "" {
			label = "a build"
		}
		work = append(work, ActiveWork{Kind: "build", ID: run.ID, Label: "Build " + label})
	}
	for _, run := range s.playLive.Running() {
		work = append(work, ActiveWork{Kind: "build_and_run", ID: run.ID, Label: run.Label})
	}
	for _, digest := range s.games.coordination.Downloading() {
		short := digest
		if len(short) > 12 {
			short = short[:12]
		}
		work = append(work, ActiveWork{Kind: "join_download", ID: digest, Label: "Downloading a game's map (" + short + ")"})
	}
	return work
}

// cancelWork stops each item the way its own Stop button does.
func (s *Server) cancelWork(work []ActiveWork) {
	for _, item := range work {
		switch item.Kind {
		case "job", "game", "hosted_game":
			if s.jobs != nil {
				if _, err := s.jobs.Cancel(item.ID); err != nil {
					s.logf("quit: cancelling job %s: %v", item.ID, err)
				}
			}
		}
	}
	s.playLive.CancelAll()
	for _, run := range s.builds.active() {
		run.cancel()
	}
}

// Close stops what this process started, in the order that loses least. Called
// by the server's own shutdown — Quit, Ctrl+C, the last page closing — so a
// Companion that is stopping does not leave a compiler running, a listing
// advertising a game that is about to end, or half a mod installed.
//
//  1. Hosted games' listings end NOW, with `host_stopped`: the owner stopped
//     the Companion, and saying so is better than leaving AUB to conclude it
//     from a missed heartbeat two minutes later.
//  2. Build & Run sequences and builds are cancelled. A sequence unstages what
//     it installed on its way out, so it is waited for, within a bound.
//
// Jobs themselves — the compiler, the running game — are stopped by the job
// service's own Close after this, gracefully first and forcefully after its
// grace, which is the path Ctrl+C has always taken.
func (s *Server) Close() {
	s.endHostedGames()
	s.playLive.CancelAll()
	// A Quake III run that is still waiting for its engine's word stops
	// waiting, and stops the engine it started on its way out.
	s.q3runs.cancelWaiting()
	for _, run := range s.builds.active() {
		run.cancel()
	}
	deadline := time.Now().Add(DrainTimeout)
	if !s.playLive.WaitIdle(DrainTimeout) {
		s.logf("shutdown: a Build & Run sequence did not finish unstaging within %s", DrainTimeout)
	}
	for len(s.builds.active()) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
}

// endHostedGames ends every listing this process is keeping alive.
func (s *Server) endHostedGames() {
	s.hosting.mu.Lock()
	advertiser := s.hosting.advertiser
	s.hosting.mu.Unlock()
	if advertiser == nil {
		return
	}
	active := advertiser.Active()
	if len(active) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostedLeaseEndTimeout)
	defer cancel()
	started := time.Now()
	advertiser.Close(ctx, aub.ReasonHostStopped)
	s.logf("shutdown: ended %d hosted-game listing(s) in %s", len(active), time.Since(started).Round(time.Millisecond))
}
