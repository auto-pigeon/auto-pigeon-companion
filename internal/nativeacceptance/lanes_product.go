package nativeacceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile/builtin"
)

// toolProfileDocument is the profile a person would write, with one option's
// default supplied so the same document can be re-authored with one field
// changed — which is how this lane produces a second digest without changing
// what the profile is allowed to do.
//
// The program it declares is THIS PROGRAM. On a clean machine there is nothing
// else portable to point at: a shell stub is a POSIX shell script and a batch
// file is not, and `AUT/AUCOM 219`'s tool-profile lane skipped itself on
// Windows for exactly that reason. `companion acceptance noise` exists so the
// approval walk needs nothing installed, on any platform this builds for.
func toolProfileDocument(text string) map[string]any {
	return map[string]any{
		"schema_version": "aucom.profile/1.1",
		"kind":           "tool",
		"id":             ToolProfileID,
		"version":        "1.0.0",
		"name":           "The native acceptance kit's own tool",
		"summary":        "A tool profile the acceptance kit writes, so the approval path can be walked with nothing installed.",
		"description": "Written by `companion acceptance run` into a directory that run owns, granted, " +
			"run, re-granted and withdrawn, and removed with the rest of that directory. It declares " +
			"one program — this one — and the smallest set of actions that still exercises the " +
			"approval path and the log bound.",
		"publisher":    map[string]any{"name": "Auto-Pigeon acceptance"},
		"license":      map[string]any{"spdx": "MIT", "name": "MIT License"},
		"tool_version": "0.0.0-acceptance",
		"platforms": []any{map[string]any{
			"platform": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH},
			"status":   "supported",
		}},
		"acquisition": []any{map[string]any{
			"mode":  "user_path",
			"title": "The copy I already have",
			"hint":  "choose the folder the program is in",
		}},
		"executables": []any{map[string]any{
			"name":  "companion",
			"title": "The Companion itself",
			"file":  "companion{platform.exe_suffix}",
		}},
		"actions": []any{
			map[string]any{
				"id": "say", "title": "Say something", "executable": "companion",
				"args": []any{"acceptance", "noise", "--say", "{option.text}"},
				"options": []any{map[string]any{
					"name": "text", "title": "Text", "type": "text",
					"default": text, "max_length": 64,
				}},
				"roots": []any{map[string]any{
					"role": "workspace", "access": "read_write",
					"purpose": "the scratch folder the executor makes for this job",
				}},
				"timeout_seconds": 60,
			},
			map[string]any{
				"id": "noise", "title": "Write a lot", "executable": "companion",
				"args": []any{"acceptance", "noise", "--lines", "{option.lines}", "--both"},
				"options": []any{map[string]any{
					"name": "lines", "title": "Lines", "type": "text",
					"default": "10", "max_length": 12,
				}},
				"roots": []any{map[string]any{
					"role": "workspace", "access": "read_write",
					"purpose": "the scratch folder the executor makes for this job",
				}},
				"timeout_seconds": 600,
			},
			map[string]any{
				"id": "wait", "title": "Stay running", "executable": "companion",
				"args": []any{"acceptance", "noise", "--seconds", "{option.seconds}"},
				"options": []any{map[string]any{
					"name": "seconds", "title": "Seconds", "type": "text",
					"default": "30", "max_length": 6,
				}},
				"roots": []any{map[string]any{
					"role": "workspace", "access": "read_write",
					"purpose": "the scratch folder the executor makes for this job",
				}},
				"timeout_seconds": 120,
			},
		},
	}
}

// --- profile ----------------------------------------------------------------

func (r *run) laneProfile(ctx context.Context) Lane {
	lane := Lane{}
	add := func(observation Observation) { lane.Observations = append(lane.Observations, observation) }

	configDir, err := r.resolveConfigDir(ctx)
	if err != nil {
		lane.State = NotAvailable
		lane.Reason = "the configuration directory could not be resolved: " + r.redact.Line(err.Error())
		return lane
	}
	toolDir := filepath.Join(r.work, "tool")
	if err := r.stageSelf(toolDir); err != nil {
		lane.State = NotAvailable
		lane.Reason = "this program could not be staged as the profile's program: " + r.redact.Line(err.Error())
		return lane
	}
	profiles := filepath.Join(configDir, "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		lane.State = NotAvailable
		lane.Reason = r.redact.Line(err.Error())
		return lane
	}
	authored := filepath.Join(r.work, "native-kit.tool.json")
	installed := filepath.Join(profiles, "native-kit.tool.json")
	write := func(text string) error {
		data, err := json.MarshalIndent(toolProfileDocument(text), "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if err := os.WriteFile(authored, data, 0o644); err != nil {
			return err
		}
		return os.WriteFile(installed, data, 0o644)
	}
	if err := write("first-version"); err != nil {
		lane.State = NotAvailable
		lane.Reason = r.redact.Line(err.Error())
		return lane
	}

	valid := r.exec(ctx, 60*time.Second, "profile", "validate", authored)
	if valid.code != 0 {
		add(failed("the document a person wrote validates", r.redact.Line(valid.output())))
		return lane
	}
	add(pass("the document a person wrote validates", r.redact.Line(firstLine(valid.stdout))))

	bind := r.exec(ctx, 120*time.Second, "acquire", "resolve", installed,
		"--mode", "user_path", "--user-path", toolDir, "--bind")
	if bind.code != 0 {
		add(failed("the program is bound to the profile", r.redact.Line(bind.output())))
		return lane
	}
	add(pass("importing a document and binding a program is one act each",
		"the document is in the profile folder and the program is recorded"))

	refused := r.exec(ctx, 60*time.Second, "job", "run", "--profile", ToolProfileID, "--action", "say")
	add(verdict(refused.code != 0 && strings.Contains(refused.output(), "reviewed"),
		"binding grants nothing: running is refused, and the refusal says what is missing",
		fmt.Sprintf("exit %d", refused.code),
		r.redact.Line(refused.output())))

	first, ok := r.review(ctx)
	if !ok {
		add(failed("the review says what it would be allowed to do, and that it may not",
			"the review could not be read"))
		return lane
	}
	add(verdict(!first.Authorized && len(first.Permissions) > 0,
		"the review says what it would be allowed to do, and that it may not",
		fmt.Sprintf("%d permissions, authorized=false", len(first.Permissions)),
		fmt.Sprintf("authorized=%t with %d permissions", first.Authorized, len(first.Permissions))))

	noDecision := r.exec(ctx, 60*time.Second, "profile", "grant", ToolProfileID, "--digest="+first.Digest)
	add(verdict(noDecision.code == 2,
		"a grant without an explicit approval is refused as a bad invocation",
		"exit 2",
		fmt.Sprintf("exit %d: %s", noDecision.code, r.redact.Line(noDecision.output()))))

	stale := r.exec(ctx, 60*time.Second, "profile", "grant", ToolProfileID,
		"--digest=sha256:"+strings.Repeat("0", 64), "--approve")
	add(verdict(stale.code != 0,
		"a grant of a digest that is not the document on this machine is refused",
		fmt.Sprintf("exit %d", stale.code),
		"it accepted a digest the document does not have"))

	granted := r.exec(ctx, 60*time.Second, "profile", "grant", ToolProfileID,
		"--digest="+first.Digest, "--approve")
	if granted.code != 0 {
		add(failed("approving the exact bytes is what makes it runnable",
			r.redact.Line(granted.output())))
		return lane
	}
	ran := r.exec(ctx, 120*time.Second, "job", "run", "--profile", ToolProfileID, "--action", "say")
	add(verdict(ran.code == 0 && strings.Contains(ran.output(), "first-version"),
		"approving the exact bytes is what makes it runnable, and it ran",
		"the program printed the option it was given",
		fmt.Sprintf("exit %d: %s", ran.code, r.redact.Line(ran.output()))))

	if err := write("second-version"); err != nil {
		add(failed("changing the document moves its digest", r.redact.Line(err.Error())))
		return lane
	}
	second, ok := r.review(ctx)
	add(verdict(ok && second.Digest != first.Digest,
		"changing the document moves its digest",
		"the two digests differ",
		"the digest did not move when the document did"))

	afterEdit := r.exec(ctx, 60*time.Second, "job", "run", "--profile", ToolProfileID, "--action", "say")
	add(verdict(afterEdit.code != 0,
		"the approval was of bytes, so the edited document is refused",
		fmt.Sprintf("exit %d", afterEdit.code),
		"the edited document ran under the old approval"))

	replay := r.exec(ctx, 60*time.Second, "profile", "grant", ToolProfileID,
		"--digest="+first.Digest, "--approve")
	add(verdict(replay.code != 0,
		"the previous approval cannot be replayed onto the new document",
		fmt.Sprintf("exit %d", replay.code),
		"the old digest was accepted for the new document"))

	regrant := r.exec(ctx, 60*time.Second, "profile", "grant", ToolProfileID,
		"--digest="+second.Digest, "--approve")
	reran := r.exec(ctx, 120*time.Second, "job", "run", "--profile", ToolProfileID, "--action", "say")
	add(verdict(regrant.code == 0 && reran.code == 0 && strings.Contains(reran.output(), "second-version"),
		"approving the new bytes makes it runnable as itself",
		"the program printed the new option",
		r.redact.Line(regrant.output()+reran.output())))

	withdraw := r.exec(ctx, 60*time.Second, "profile", "withdraw", ToolProfileID, "--confirm")
	afterWithdraw := r.exec(ctx, 60*time.Second, "job", "run", "--profile", ToolProfileID, "--action", "say")
	add(verdict(withdraw.code == 0 && afterWithdraw.code != 0,
		"withdrawing the approval stops it running again",
		fmt.Sprintf("withdrawn, then exit %d", afterWithdraw.code),
		r.redact.Line(withdraw.output()+afterWithdraw.output())))

	stillBound, ok := r.review(ctx)
	add(verdict(ok && !stillBound.Authorized,
		"the recorded program survives the withdrawal, because it was never the grant",
		"the profile is still known and is no longer authorized",
		"the profile could not be reviewed after the withdrawal"))

	r.toolProfileID = ToolProfileID
	r.toolGranted = false
	return lane
}

// reviewDocument is the part of `profile review --json` this reads.
type reviewDocument struct {
	Digest      string   `json:"digest"`
	Authorized  bool     `json:"authorized"`
	Permissions []string `json:"permissions"`
}

func (r *run) review(ctx context.Context) (reviewDocument, bool) {
	res := r.exec(ctx, 60*time.Second, "profile", "review", ToolProfileID, "--json")
	if res.code != 0 {
		return reviewDocument{}, false
	}
	var document reviewDocument
	if err := json.Unmarshal([]byte(res.stdout), &document); err != nil {
		// `permissions` may be a list of objects rather than of strings; the
		// only member this lane needs to count is how many there are.
		var loose struct {
			Digest      string `json:"digest"`
			Authorized  bool   `json:"authorized"`
			Permissions []any  `json:"permissions"`
		}
		if err := json.Unmarshal([]byte(res.stdout), &loose); err != nil {
			return reviewDocument{}, false
		}
		document = reviewDocument{Digest: loose.Digest, Authorized: loose.Authorized}
		for range loose.Permissions {
			document.Permissions = append(document.Permissions, "")
		}
	}
	return document, document.Digest != ""
}

// stageSelf copies this program into a directory under the name the authored
// profile declares, so `acquire resolve --mode user_path` finds it the way it
// finds any copy a user already has.
func (r *run) stageSelf(directory string) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	name := "companion"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	destination := filepath.Join(directory, name)
	source, err := os.Open(r.executable)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	return target.Close()
}

// --- toolchain --------------------------------------------------------------

func (r *run) laneToolchain(ctx context.Context) Lane {
	lane := Lane{}
	add := func(observation Observation) { lane.Observations = append(lane.Observations, observation) }

	// The built-in document, written out of this binary. A clean machine has no
	// checkout, so the file `acquire resolve` reads has to come from the
	// program that ships it — as its CANONICAL bytes, which is what the digest
	// this build already knows is taken over.
	entry, err := builtin.Find(Q1ToolProfileID)
	if err != nil {
		lane.State = NotAvailable
		lane.Reason = "this build ships no " + Q1ToolProfileID + " profile"
		return lane
	}
	canonical, err := profile.Canonical(entry.Profile)
	if err != nil {
		lane.State = NotAvailable
		lane.Reason = r.redact.Line(err.Error())
		return lane
	}
	document := filepath.Join(r.work, "ericw-tools-q1.tool.json")
	if err := os.WriteFile(document, append(canonical, '\n'), 0o644); err != nil {
		lane.State = NotAvailable
		lane.Reason = r.redact.Line(err.Error())
		return lane
	}
	add(pass("the built-in compiler profile is written out of this binary",
		"canonical bytes, digest "+shortDigest(entry.Digest)))

	// The Companion downloads no program (operator, 2026-09-23): the only way
	// to a compiler is one the operator already has.
	if r.options.ToolPath == "" {
		add(unavailable("an EricW build the operator already has is bound by user_path",
			"no --tool-path was given, so there is no build to bind"))
		return lane
	}
	bind := r.exec(ctx, 300*time.Second, "acquire", "resolve", document,
		"--mode", "user_path", "--user-path", r.options.ToolPath, "--bind")
	if bind.code == 0 {
		r.compilersBound = true
		add(pass("an EricW build the operator already has is bound by user_path",
			"the operator named the directory; nothing was downloaded"))
	} else {
		add(failed("an EricW build the operator already has is bound by user_path",
			r.redact.Line(bind.output())))
	}
	return lane
}

func shortDigest(digest string) string {
	trimmed := strings.TrimPrefix(digest, "sha256:")
	if len(trimmed) > 12 {
		return trimmed[:12]
	}
	return trimmed
}
