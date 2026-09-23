// Package threat is Auto-Pigeon Companion's threat model, written as data the
// build checks rather than as a document that goes stale.
//
// # What this program is, from an attacker's point of view
//
// It runs programs somebody else wrote — ones the user installed, and the
// extractor shipped beside it — on the user's machine, against files the user
// points it at, described by documents the user may have got from a stranger.
// It downloads maps and textures, and no program. Then it serves a local HTTP
// API that can start those programs. Every one of those clauses is an attack
// surface, and the trust boundaries between them are the whole design.
//
//	the user            trusted. Any process running as them can already read
//	                    config.json. Nothing here defends against them.
//	AUB                 authenticates and authorises. It is NOT trusted to
//	                    decide what runs: it hands out asset ids and content,
//	                    never a program, and synced bytes are verified before
//	                    they are published anywhere.
//	the extractor       shipped beside the Companion; its digest is checked
//	                    against the release's bundle manifest, and it must
//	                    speak a protocol this build drives.
//	a profile document  UNTRUSTED text. It declares a command; it cannot BE a
//	                    command. There is no shell anywhere in the executor.
//	an archive          UNTRUSTED bytes. Every name and every size is checked
//	                    against the destination before anything is written.
//	a tool's output     UNTRUSTED bytes. Bounded, never re-executed, and
//	                    stripped of control characters before a person reads it.
//	a web page          hostile by default. The local API answers no preflight
//	                    and has no ambient credential.
//
// # Why the matrix is code
//
// Each row names the tests that are its evidence, by package and function.
// [Check] fails when a row has no evidence, when a named test no longer exists
// in this repository, or when an accepted residual risk has no owner or its
// review date has passed. That last one is not a defect in the test: a residual
// risk with an expiry that nobody has to look at again is an accepted risk
// nobody accepted.
//
// A row is not a claim that something is impossible. It is a claim that a named
// test would fail if the mitigation were removed, which is the only kind of
// claim about security that survives a refactor.
package threat

import "time"

// Category groups the rows. The set is fixed: it is the list the hardening
// prompt enumerated, plus the recovery and release work that came with it, and
// [Check] fails when a category has no row at all.
type Category string

const (
	CatDocument   Category = "malicious-document"
	CatSupply     Category = "supply-chain"
	CatArchive    Category = "archive"
	CatExecution  Category = "execution"
	CatLocalAPI   Category = "local-api"
	CatCredential Category = "credential"
	CatDeepLink   Category = "deep-link"
	CatRace       Category = "concurrency"
	CatRecovery   Category = "recovery"
	CatRelease    Category = "release"
)

// Categories is every category, in reporting order.
var Categories = []Category{
	CatDocument, CatSupply, CatArchive, CatExecution, CatLocalAPI,
	CatCredential, CatDeepLink, CatRace, CatRecovery, CatRelease,
}

// Evidence is one automated test that would fail if a mitigation were removed.
type Evidence struct {
	// Package is the import path below the module root, e.g. "internal/pack".
	Package string `json:"package"`
	// Test is the function name.
	Test string `json:"test"`
}

// Residual is an accepted risk: something this design does not stop, written
// down with somebody's name on it and a date by which it is looked at again.
type Residual struct {
	// What is not stopped, in one sentence a person outside this project can
	// act on.
	What string `json:"what"`
	// Why it is accepted rather than fixed.
	Why string `json:"why"`
	// Owner is who accepted it. A residual risk with no owner is one nobody
	// accepted.
	Owner string `json:"owner"`
	// Review is when it is looked at again, as YYYY-MM-DD. [Check] fails once
	// it has passed, which is the mechanism and not a bug.
	Review string `json:"review"`
}

// Row is one threat.
type Row struct {
	ID       string   `json:"id"`
	Category Category `json:"category"`
	// Title is the threat in one line, from the attacker's side.
	Title string `json:"title"`
	// Asset is what is at stake if it works.
	Asset string `json:"asset"`
	// Vector is how it is attempted.
	Vector string `json:"vector"`
	// Mitigation is what stops it, and where that lives.
	Mitigation string `json:"mitigation"`
	// Evidence is the automated proof. A row with none needs a Manual.
	Evidence []Evidence `json:"evidence,omitempty"`
	// Manual is a precise procedure for a row nothing automated can cover —
	// one that names the machine, the steps and the observation, not "test it".
	Manual string `json:"manual,omitempty"`
	// Residual is what remains after the mitigation, when anything does.
	Residual *Residual `json:"residual,omitempty"`
}

// Covered reports whether a row has any evidence at all.
func (r Row) Covered() bool { return len(r.Evidence) > 0 || r.Manual != "" }

// Matrix is the whole model.
//
// Ordered by category and then by id, which is display order and also the order
// somebody reads them in: a document arrives, it names a program, the program is
// found on this machine, an archive is unpacked, a process runs, the API is
// served.
func Matrix() []Row { return append([]Row(nil), rows...) }

// Find returns one row.
func Find(id string) (Row, bool) {
	for _, row := range rows {
		if row.ID == id {
			return row, true
		}
	}
	return Row{}, false
}

// InCategory returns the rows in one category.
func InCategory(category Category) []Row {
	var out []Row
	for _, row := range rows {
		if row.Category == category {
			out = append(out, row)
		}
	}
	return out
}

// Expired lists the residual risks whose review date has passed.
func Expired(now time.Time) []Row {
	var out []Row
	for _, row := range rows {
		if row.Residual == nil {
			continue
		}
		review, err := time.Parse("2006-01-02", row.Residual.Review)
		if err != nil || !review.After(now.UTC()) {
			out = append(out, row)
		}
	}
	return out
}
