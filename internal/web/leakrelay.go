package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
)

// What the editor is told, and what a build remembers about returning its
// result (`NEW_307W`).
//
// The editor tab that asked for a leak test cannot ask this program anything: a
// page may not call the reader's loopback. So this program says what it is
// doing to the account's own server, one small status at a time, and the editor
// reads that. It is progress and never evidence — the result bundle is still
// the only thing the editor imports — and a status that cannot be sent (signed
// out, AUB away) is simply not sent: nothing here waits on it or fails for it.

// The states AUB's relay accepts. The stage beside one says what it is doing.
const (
	leakReceived     = "received"
	leakReviewing    = "reviewing"
	leakBuilding     = "building"
	leakReturning    = "returning"
	leakReturned     = "returned"
	leakReturnFailed = "return_failed"
	leakDismissed    = "dismissed"
	leakBlocked      = "blocked"
)

// reportLeakStatus tells the editor's tab, through AUB, what happened to its
// request. Best effort: it returns at once and its failure changes nothing.
// The sending, the retrying and the order are leaksender.go's.
func (s *Server) reportLeakStatus(link aub.LeakTestLink, state, stage, buildID string) {
	s.leaks.report(link, state, truncateLeakStage(stage), buildID, state == leakReceived)
}

// truncateLeakStage keeps a stage inside AUB's limit, which counts characters:
// cutting bytes could split one and send text AUB cannot read.
func truncateLeakStage(stage string) string {
	stage = strings.ToValidUTF8(stage, "\uFFFD")
	if utf8.RuneCountInString(stage) <= leakStageLimit {
		return stage
	}
	return string([]rune(stage)[:leakStageLimit])
}

// leakStageLimit is AUB's `maxLeakStatusStage`.
const leakStageLimit = 120

// leakSession is the account this program would post as right now.
func (s *Server) leakSession() leakSession {
	s.adoptSessionFromDisk()
	s.mu.RLock()
	client, session := s.client, s.settings.Session
	s.mu.RUnlock()
	// An expired token is no session: posting with it would only be refused.
	if client == nil || !client.Authenticated() || client.SessionExpired(time.Now()) {
		return leakSession{}
	}
	return leakSession{Server: client.BaseURL(), UserID: session.UserID, Token: client.Token(), Send: client.PublishLeakStatus}
}

// reportLeakBuild says which step of a leak build is running.
func (s *Server) reportLeakBuild(link aub.LeakTestLink, manifest *build.Manifest) {
	if manifest == nil || manifest.State.Terminal() {
		return
	}
	stage := "starting"
	for _, step := range manifest.Steps {
		if step.State == job.Running {
			stage = step.ID
			break
		}
	}
	s.reportLeakStatus(link, leakBuilding, stage, manifest.BuildID)
}

// handleLeakTestRequest is the pending request as this machine holds it, and
// nothing else: no AUB call, so an open page can ask every couple of seconds
// whether a link arrived. The first time a request is seen it is acknowledged
// to the editor.
func (s *Server) handleLeakTestRequest(w http.ResponseWriter, _ *http.Request) {
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
	s.reportLeakStatus(*request, leakReceived, "", "")
	// `relay` is whether the editor has been told: a request this machine
	// holds and the editor has not heard about are different facts.
	writeJSON(w, http.StatusOK, map[string]any{
		"pending": true, "request_id": request.RequestID, "asset_id": request.AssetID,
		"revision": request.Revision, "content_sha256": request.ContentSHA256, "received_at": received,
		"relay": s.leaks.view(request.RequestID),
	})
}

// pendingLeakRequest is the pending request when it is the one named.
func (s *Server) pendingLeakRequest(requestID string) *aub.LeakTestLink {
	dir, err := s.configDir()
	if err != nil || requestID == "" {
		return nil
	}
	request, _, err := leakintent.Read(leakintent.Path(dir), time.Now().UTC())
	if err != nil || request == nil || request.RequestID != requestID {
		return nil
	}
	return request
}

// handleLeakTestReviewing records that a person opened the review. It starts
// nothing; the editor is told its request is being looked at.
func (s *Server) handleLeakTestReviewing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequestID string `json:"request_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	request := s.pendingLeakRequest(body.RequestID)
	if request == nil {
		writeError(w, http.StatusConflict, errors.New("that leak request is no longer pending; ask the editor again"))
		return
	}
	s.reportLeakStatus(*request, leakReviewing, "", "")
	writeJSON(w, http.StatusOK, map[string]any{"reviewing": true})
}

// --- returning the result ---------------------------------------------------

// leakReturnFile is kept in the build's own directory: which request the build
// answers and how returning its result went. The compiler's verdict is the
// manifest's; this is only the delivery, so a failed delivery can be retried
// without compiling anything again.
const leakReturnFile = "leak-return.json"

const leakReturnSchema = "aucom.leak-return/1.0"

type leakReturn struct {
	SchemaVersion string           `json:"schema_version"`
	Request       aub.LeakTestLink `json:"request"`
	// State is `pending` while the build runs, then `sending`, `returned` or
	// `failed`.
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	Attempts  int       `json:"attempts"`
	UpdatedAt time.Time `json:"updated_at"`
}

var leakReturnLock sync.Mutex

func (s *Server) leakReturnPath(buildID string) (string, error) {
	dir, err := s.buildsDir()
	if err != nil {
		return "", err
	}
	if _, err := build.Find(dir, buildID); err != nil {
		return "", err
	}
	return filepath.Join(dir, buildID, leakReturnFile), nil
}

func (s *Server) readLeakReturn(buildID string) (*leakReturn, error) {
	path, err := s.leakReturnPath(buildID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record leakReturn
	if json.Unmarshal(raw, &record) != nil || record.SchemaVersion != leakReturnSchema || record.Request.RequestID == "" {
		return nil, errors.New("this build's return record cannot be read")
	}
	return &record, nil
}

func (s *Server) writeLeakReturn(buildID string, record leakReturn) error {
	path, err := s.leakReturnPath(buildID)
	if err != nil {
		return err
	}
	record.SchemaVersion, record.UpdatedAt = leakReturnSchema, time.Now().UTC()
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// deliverLeakResult returns a finished build's result to the editor and
// records how that went. It never compiles: a retry sends the same result of
// the same build, and AUB treats identical bytes as the same delivery.
func (s *Server) deliverLeakResult(buildID string, request aub.LeakTestLink) leakReturn {
	leakReturnLock.Lock()
	defer leakReturnLock.Unlock()
	record := leakReturn{Request: request, State: "sending"}
	if previous, err := s.readLeakReturn(buildID); err == nil && previous != nil {
		record.Attempts = previous.Attempts
	}
	record.Attempts++
	_ = s.writeLeakReturn(buildID, record)
	s.reportLeakStatus(request, leakReturning, "", buildID)
	if err := s.publishLeakResult(buildID, request.RequestID); err != nil {
		record.State, record.Error = "failed", err.Error()
		s.reportLeakStatus(request, leakReturnFailed, record.Error, buildID)
	} else {
		record.State = "returned"
		s.reportLeakStatus(request, leakReturned, "", buildID)
	}
	_ = s.writeLeakReturn(buildID, record)
	return record
}

// handleLeakTestReturn says how returning a build's result went.
func (s *Server) handleLeakTestReturn(w http.ResponseWriter, r *http.Request) {
	record, err := s.readLeakReturn(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if record == nil {
		writeJSON(w, http.StatusOK, map[string]any{"requested": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"requested": true, "state": record.State, "error": record.Error, "attempts": record.Attempts,
		"updated_at": record.UpdatedAt, "request_id": record.Request.RequestID,
		"asset_id": record.Request.AssetID, "revision": record.Request.Revision,
	})
}

// handleLeakTestReturnRetry sends a finished build's result again.
func (s *Server) handleLeakTestReturnRetry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	record, err := s.readLeakReturn(id)
	if err != nil || record == nil {
		writeError(w, http.StatusConflict, errors.New("this build was not started for an editor's leak request, so there is nowhere to return it"))
		return
	}
	if record.State == "pending" || record.State == "sending" {
		writeError(w, http.StatusConflict, errors.New("this build's result is not ready to be sent again yet"))
		return
	}
	// A person asked: a status held back by a refused session is tried again.
	s.leaks.retryNow(record.Request.RequestID)
	after := s.deliverLeakResult(id, record.Request)
	if after.State != "returned" {
		writeError(w, http.StatusBadGateway, errors.New(after.Error))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requested": true, "state": after.State, "attempts": after.Attempts})
}
