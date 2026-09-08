package nativeacceptance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Schema is the result bundle's format identifier, carried in the document so a
// reader takes it for what it is rather than guessing from the shape.
const Schema = "aucom.native-acceptance/1.0"

// KitVersion is the operator kit's own version, so a merge on the development
// machine can tell a bundle produced by an older kit from one produced by this
// one. It moves when what a lane MEANS changes, not when a message is reworded.
const KitVersion = "1"

// State is what a lane or one of its observations came to.
//
// Five values, and the last three are not synonyms for a pass. `AGENTS.md` §3e
// and §3g both turn on this: a check nobody ran counted as a pass is how a
// suite comes to report green over a measurement nobody made.
type State string

const (
	// Pass: the claim was checked on this machine and held.
	Pass State = "pass"
	// Fail: the claim was checked on this machine and did not hold.
	Fail State = "fail"
	// Skipped: the operator asked for it not to run.
	Skipped State = "skipped"
	// NotApplicable: this platform does not have the thing being claimed.
	NotApplicable State = "not_applicable"
	// NotAvailable: a prerequisite is simply not installed here.
	NotAvailable State = "not_available"
)

// states is the closed set a document may carry. A value outside it fails
// validation rather than being rendered as an unknown word.
var states = map[State]bool{
	Pass: true, Fail: true, Skipped: true, NotApplicable: true, NotAvailable: true,
}

// Counts is what a reader adds up without re-walking the lanes.
type Counts struct {
	Pass          int `json:"pass"`
	Fail          int `json:"fail"`
	Skipped       int `json:"skipped"`
	NotApplicable int `json:"not_applicable"`
	NotAvailable  int `json:"not_available"`
}

// Observation is one claim, and what happened to it.
//
// Detail is the only free-text member in the whole document and is the reason
// [Redactor] exists. Everything else is a typed fact.
type Observation struct {
	Claim  string `json:"claim"`
	State  State  `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// Lane is one part of the operator's run.
type Lane struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	State        State         `json:"state"`
	Reason       string        `json:"reason,omitempty"`
	ElapsedMS    int64         `json:"elapsed_ms"`
	Observations []Observation `json:"observations"`
}

// PlatformFacts is what machine this ran on. No hostname, no user, no domain:
// the merge needs an artifact target and a runtime, and nothing else here is
// needed for a verdict.
type PlatformFacts struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Target   string `json:"target"`
	Runtime  string `json:"runtime"`
	CPUs     int    `json:"cpus"`
	Endian   string `json:"endian"`
	Cgo      bool   `json:"cgo"`
	PathSep  string `json:"path_separator"`
	ExeSufix string `json:"exe_suffix"`
}

// KitFacts is how the run was started and what the shell vouched for before it
// started anything.
//
// ChecksumsVerified is the shell's answer rather than the binary's, and that is
// the whole reason the kit is a shell script: a program cannot vouch for its own
// bytes, so the check has to happen in something that ran first.
type KitFacts struct {
	EntryPoint        string `json:"entry_point"`
	KitVersion        string `json:"kit_version"`
	ChecksumsVerified State  `json:"checksums_verified"`
	ChecksumDetail    string `json:"checksum_detail,omitempty"`
	Shell             string `json:"shell,omitempty"`
}

// GameRow is the optional owned-game lane's record.
//
// `AUT/AUCOM 231` names the complete list: game family, the profile and its
// version, the native platform, the command SHAPE with paths replaced, the exit
// or ready signal, and the elapsed time. There is no member a byte of game data,
// a filename or a directory could travel in, and there is no flag that adds one.
//
// Preview and launch are separate rows because they are separate claims: a
// preview that printed a command is not evidence that an engine started.
type GameRow struct {
	// Row is "preview" or "launch".
	Row string `json:"row"`
	// Family is AUB's engine family: quake1, quake2, quake3.
	Family         string `json:"family"`
	ProfileID      string `json:"profile_id"`
	ProfileVersion string `json:"profile_version"`
	Platform       string `json:"platform"`
	// CommandShape is the argv with every path replaced by its role.
	CommandShape []string `json:"command_shape"`
	State        State    `json:"state"`
	// Signal is the exit code or the ready signal, as a word: "printed",
	// "ready", "exited:0", "not_run".
	Signal    string `json:"signal"`
	ElapsedMS int64  `json:"elapsed_ms"`
	Detail    string `json:"detail,omitempty"`
}

// Bundle is the whole publishable result. Everything in it is a typed fact this
// program put there on purpose; see the package comment for why nothing is
// gathered and then filtered.
type Bundle struct {
	Schema           string        `json:"schema"`
	BundleID         string        `json:"bundle_id"`
	ProducedAt       string        `json:"produced_at"`
	CompanionVersion string        `json:"companion_version"`
	Kit              KitFacts      `json:"kit"`
	Platform         PlatformFacts `json:"platform"`
	Lanes            []Lane        `json:"lanes"`
	Game             []GameRow     `json:"game,omitempty"`
	Counts           Counts        `json:"counts"`
	// Verdict is pass only when no lane and no observation failed. A run that
	// could not check something is not a run that checked it.
	Verdict State `json:"verdict"`
}

// Target is the artifact target this platform is, as `build/release.sh` names
// one: `linux/amd64`.
func Target() string { return runtime.GOOS + "/" + runtime.GOARCH }

// Facts reports this machine, for the [Bundle.Platform] member.
func Facts() PlatformFacts {
	suffix := ""
	separator := "/"
	if runtime.GOOS == "windows" {
		suffix = ".exe"
		separator = `\`
	}
	endian := "little"
	if isBigEndian() {
		endian = "big"
	}
	return PlatformFacts{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Target:   Target(),
		Runtime:  runtime.Version(),
		CPUs:     runtime.NumCPU(),
		Endian:   endian,
		Cgo:      cgoEnabled,
		PathSep:  separator,
		ExeSufix: suffix,
	}
}

// Finish fills the derived members: the counts, and the verdict they imply.
//
// The verdict is computed rather than set, so a caller cannot write `pass` over
// a lane that failed.
func (b *Bundle) Finish(now time.Time) {
	b.Schema = Schema
	b.ProducedAt = now.UTC().Format(time.RFC3339)
	b.Kit.KitVersion = KitVersion
	b.Platform = Facts()
	counts := Counts{}
	verdict := Pass
	tally := func(state State) {
		switch state {
		case Pass:
			counts.Pass++
		case Fail:
			counts.Fail++
			verdict = Fail
		case Skipped:
			counts.Skipped++
		case NotApplicable:
			counts.NotApplicable++
		case NotAvailable:
			counts.NotAvailable++
		}
	}
	for _, lane := range b.Lanes {
		for _, observation := range lane.Observations {
			tally(observation.State)
		}
		if lane.State == Fail {
			verdict = Fail
		}
	}
	for _, row := range b.Game {
		if row.State == Fail {
			verdict = Fail
		}
	}
	b.Counts = counts
	b.Verdict = verdict
	if b.BundleID == "" {
		b.BundleID = fmt.Sprintf("%s-%s-%s",
			strings.ReplaceAll(b.Platform.Target, "/", "-"),
			now.UTC().Format("20060102T150405Z"), b.digestSeed())
	}
}

func (b *Bundle) digestSeed() string {
	sum := sha256.Sum256([]byte(b.ProducedAt + b.CompanionVersion + b.Platform.Target))
	return hex.EncodeToString(sum[:])[:12]
}

// Encode renders the document a bundle directory holds.
func (b Bundle) Encode() ([]byte, error) {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Digest is the SHA-256 of the encoded document — what `SHA256SUMS` beside it
// records, and what a merge on the development machine re-computes.
//
// A digest is not a signature and this program says so everywhere it prints
// one: anybody who can replace the document can replace the digest. What it
// makes detectable is a corrupted or truncated transfer, which is the same
// honesty `build/release.sh` already publishes SHA256SUMS under.
func Digest(document []byte) string {
	sum := sha256.Sum256(document)
	return hex.EncodeToString(sum[:])
}

// absolutePath matches what a path looks like on either family of platform: a
// POSIX path, a Windows drive path, or a UNC path — each required to START at a
// token boundary.
//
// The boundary group is not a refinement, it is the rule. Without it `/amd64`
// inside `linux/amd64` is an absolute path, and the first run of this kit
// refused to publish its own platform identity for holding one. The class
// excludes `>` as well as word characters, so the tail of a path whose root has
// already become `<game_root>` survives as the relative part it now is — which
// is what makes a redacted command shape readable rather than a row of
// placeholders.
var absolutePath = regexp.MustCompile(
	`(^|[^\w.\->/])((?:[A-Za-z]:[\\/]|\\\\[^\\/\s]+\\|/)[^\s"',;:)\]}]*)`)

// Redactor replaces the locations a run knows about with the ROLE they played,
// and then replaces anything else that still looks like a path.
//
// Two layers on purpose, and the order matters. The named roots are what makes
// a detail readable — `<game_root>/id1` says something, `<path>/id1` does not —
// and the generic sweep is what makes the absence of a leak a property of the
// type rather than of how carefully each call site was written. It is the same
// arrangement `AUT/AUCOM 230` settled on for the soak report: an allow-list
// constructs the document, and a scan is defence in depth over it.
type Redactor struct {
	roots []replacement
}

type replacement struct {
	from string
	to   string
}

// NewRedactor builds one. Roots are applied longest-first, so a nested root
// does not leave its parent's prefix behind.
func NewRedactor() *Redactor { return &Redactor{} }

// Root records one location and the role it played. An empty path is ignored,
// so a caller may register an optional root unconditionally.
func (r *Redactor) Root(path, role string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	r.roots = append(r.roots, replacement{from: path, to: "<" + role + ">"})
	sort.SliceStable(r.roots, func(i, j int) bool {
		return len(r.roots[i].from) > len(r.roots[j].from)
	})
}

// Text returns a string safe to publish: named roots become their role, and
// every remaining absolute path becomes `<path>`.
func (r *Redactor) Text(text string) string {
	if text == "" {
		return ""
	}
	out := text
	for _, root := range r.roots {
		out = strings.ReplaceAll(out, root.from, root.to)
		// Windows prints the same location with either separator depending on
		// which API produced it, so both spellings are replaced.
		out = strings.ReplaceAll(out, strings.ReplaceAll(root.from, `\`, "/"), root.to)
	}
	out = absolutePath.ReplaceAllString(out, "${1}<path>")
	return strings.TrimSpace(out)
}

// Line is [Redactor.Text] bounded to one line and a sane length, for a detail
// taken from a program's own output.
func (r *Redactor) Line(text string) string {
	cleaned := r.Text(text)
	if index := strings.IndexAny(cleaned, "\r\n"); index >= 0 {
		cleaned = cleaned[:index]
	}
	const maxDetail = 200
	if len(cleaned) > maxDetail {
		cleaned = cleaned[:maxDetail] + "…"
	}
	return cleaned
}

// Validate reports every reason a document may not be published or merged.
//
// It is run by the producer before writing and by every consumer before
// merging, in both languages this project reads bundles in. That is not
// duplication of one truth: a consumer that trusted a producer's own claim
// about itself would have no check at all.
func (b Bundle) Validate() []string {
	var problems []string
	if b.Schema != Schema {
		problems = append(problems, fmt.Sprintf("schema is %q, not %q", b.Schema, Schema))
	}
	if b.BundleID == "" {
		problems = append(problems, "bundle_id is empty")
	}
	if _, err := time.Parse(time.RFC3339, b.ProducedAt); err != nil {
		problems = append(problems, fmt.Sprintf("produced_at %q is not an RFC3339 time", b.ProducedAt))
	}
	if b.Platform.Target == "" || !strings.Contains(b.Platform.Target, "/") {
		problems = append(problems, fmt.Sprintf("platform target %q is not <os>/<arch>", b.Platform.Target))
	}
	if b.Platform.Target != b.Platform.OS+"/"+b.Platform.Arch {
		problems = append(problems, "platform target disagrees with os/arch")
	}
	if !states[b.Verdict] {
		problems = append(problems, fmt.Sprintf("verdict %q is not a state", b.Verdict))
	}
	if len(b.Lanes) == 0 {
		problems = append(problems, "no lane ran, so this bundle is evidence of nothing")
	}
	failed := false
	seen := map[string]bool{}
	for _, lane := range b.Lanes {
		if lane.ID == "" {
			problems = append(problems, "a lane has no id")
		}
		if seen[lane.ID] {
			problems = append(problems, fmt.Sprintf("lane %q appears twice", lane.ID))
		}
		seen[lane.ID] = true
		if !states[lane.State] {
			problems = append(problems, fmt.Sprintf("lane %q state %q is not a state", lane.ID, lane.State))
		}
		if lane.State == Fail {
			failed = true
		}
		for _, observation := range lane.Observations {
			if !states[observation.State] {
				problems = append(problems, fmt.Sprintf(
					"lane %q observation %q state %q is not a state", lane.ID, observation.Claim, observation.State))
			}
			if observation.State == Fail {
				failed = true
			}
		}
	}
	for _, row := range b.Game {
		if row.Row != "preview" && row.Row != "launch" {
			problems = append(problems, fmt.Sprintf("game row %q is neither preview nor launch", row.Row))
		}
		if !states[row.State] {
			problems = append(problems, fmt.Sprintf("game row %q state %q is not a state", row.Row, row.State))
		}
		if row.State == Fail {
			failed = true
		}
	}
	if failed && b.Verdict != Fail {
		problems = append(problems, "the verdict is not fail although something failed")
	}
	problems = append(problems, b.leaks()...)
	return problems
}

// leaks reports every free-text member that still holds something that looks
// like an absolute path. It is the check that makes the redactor's output a
// property of the document rather than of the code that filled it.
func (b Bundle) leaks() []string {
	var problems []string
	inspect := func(where, text string) {
		if match := absolutePath.FindString(text); match != "" {
			problems = append(problems, fmt.Sprintf(
				"%s holds what looks like an absolute path, which may not be published", where))
		}
	}
	for _, lane := range b.Lanes {
		inspect(fmt.Sprintf("lane %q reason", lane.ID), lane.Reason)
		for _, observation := range lane.Observations {
			inspect(fmt.Sprintf("lane %q observation %q", lane.ID, observation.Claim), observation.Detail)
		}
	}
	for _, row := range b.Game {
		inspect(fmt.Sprintf("game row %q detail", row.Row), row.Detail)
		for _, argument := range row.CommandShape {
			inspect(fmt.Sprintf("game row %q command shape", row.Row), argument)
		}
	}
	inspect("kit checksum detail", b.Kit.ChecksumDetail)
	return problems
}
