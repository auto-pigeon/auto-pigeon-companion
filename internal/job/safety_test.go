package job

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The payloads. Each is a thing that would be interesting to a shell, and none
// of them ever meets one — see the package comment in exec.go.
var injectionPayloads = []string{
	"e1m1; touch pwned",
	"e1m1 && touch pwned",
	"e1m1 | touch pwned",
	"$(touch pwned)",
	"`touch pwned`",
	"e1m1\ntouch pwned",
	"e1m1 > pwned",
	"../../etc/passwd",
	"%SystemRoot%\\system32\\calc.exe",
	"'; DROP TABLE jobs; --",
}

func TestAnInjectionPayloadIsOneLiteralArgvElementAndNeverRuns(t *testing.T) {
	action := fixtureAction{
		ID:          "play_map",
		Title:       "Play a map",
		Executable:  "helper",
		Args:        []any{helperFlag, "argv", "{runtime.map_name}"},
		Roots:       []map[string]any{{"role": "workspace", "access": "read_write", "purpose": "scratch space"}},
		Timeout:     60,
		SuccessCode: nil,
	}
	action.Environment = nil
	document := fixtureEngineProfile(t, "test.injection.engine", withSessionRole(action, "client"))
	h := newHarness(t, document)

	for _, payload := range injectionPayloads {
		t.Run(payload, func(t *testing.T) {
			request := h.helperRequest("test.injection.engine", "play_map")
			request.Runtime = map[string]string{"map_name": payload}
			finished := h.runToEnd(request)

			if finished.State != Succeeded {
				t.Fatalf("state = %s, want succeeded (error: %s)", finished.State, finished.Error)
			}
			// The helper prints one line per argv element. The payload is one
			// element, byte for byte, with nothing split off it.
			lines := strings.Split(strings.TrimSuffix(h.mustLog(finished.ID, "stdout"), "\n"), "\n")
			want := strings.Split(payload, "\n") // A newline inside one argv element still prints as two lines.
			if len(lines) != len(want) {
				t.Fatalf("the program received %d argv lines %q, want the payload as one element", len(lines), lines)
			}
			for i := range want {
				if lines[i] != want[i] {
					t.Errorf("argv[%d] = %q, want %q", i, lines[i], want[i])
				}
			}
			// And nothing a shell would have done, happened.
			for _, dir := range []string{finished.Workspace, filepath.Dir(finished.Workspace), h.dir} {
				if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
					t.Fatalf("the payload %q created %s: something interpreted it", payload, filepath.Join(dir, "pwned"))
				}
			}
			if finished.Command == nil || len(finished.Command.Args) != 3 {
				t.Fatalf("the recorded argv is %v, want three elements", finished.Command)
			}
			if finished.Command.Args[2] != payload {
				t.Errorf("the recorded argv[2] = %q, want the payload verbatim", finished.Command.Args[2])
			}
		})
	}
}

func TestAnOptionValueThatIsNotItsTypeIsRefusedBeforeAnythingRuns(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.coercion", writeAction()))

	for name, value := range map[string]string{
		"a path in a text option":                     "../../etc/passwd",
		"shell syntax in a text option":               "hello; rm -rf ~",
		"an undeclared option":                        "",
		"a value over the length limit":               strings.Repeat("a", 200),
		"a null byte in a text option":                "hello\x00world",
		"a space, which a text option does not allow": "hello world",
	} {
		t.Run(name, func(t *testing.T) {
			request := h.helperRequest("test.coercion", "write")
			if name == "an undeclared option" {
				request.Options = map[string]string{"not-declared": "x"}
			} else {
				request.Options = map[string]string{"text": value}
			}
			if _, err := h.service.Submit(request); err == nil {
				t.Fatal("the request was accepted; an illegal value must be refused before a job exists")
			}
		})
	}

	// Nothing was queued: a refused request leaves no record behind.
	jobs, err := h.service.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("%d jobs exist after only refused requests", len(jobs))
	}
}

func TestAnUnresolvedPlaceholderIsRefusedRatherThanPassedThrough(t *testing.T) {
	// `{runtime.map_name}` with nothing supplying it. The resolver must refuse
	// rather than hand the literal text to the program.
	action := withSessionRole(fixtureAction{
		ID:         "play_map",
		Title:      "Play a map",
		Executable: "helper",
		Args:       []any{helperFlag, "argv", "{runtime.map_name}"},
		Roots:      []map[string]any{{"role": "workspace", "access": "read_write", "purpose": "scratch space"}},
		Timeout:    60,
	}, "client")
	h := newHarness(t, fixtureEngineProfile(t, "test.unresolved.engine", action))

	_, err := h.service.Submit(h.helperRequest("test.unresolved.engine", "play_map"))
	if err == nil {
		t.Fatal("a request with an unresolved placeholder was accepted")
	}
	if !strings.Contains(err.Error(), "map_name") {
		t.Errorf("error = %v, want it to name the placeholder that could not be resolved", err)
	}
}

func TestASymlinkedOutputIsNotPublished(t *testing.T) {
	if err := os.Symlink(filepath.Join(t.TempDir(), "target"), filepath.Join(t.TempDir(), "probe")); err != nil {
		t.Skipf("this platform cannot create symlinks: %v", err)
	}

	// Two links, refused by two different checks, and both are worth having.
	//
	// A link out of the workspace is caught by containment: the path resolves
	// somewhere the action never declared. A link that stays *inside* the
	// workspace resolves fine and is caught by the collector instead, which
	// publishes regular files and nothing else — because a tool that hands back
	// a link rather than a file is a tool whose artifact is a path, and a path
	// means something different wherever the artifact is later opened.
	//
	// The targets are relative because the validator refuses an absolute path
	// in a document: a machine path in a portable document is a fault of its
	// own. Four levels up from `<store>/<job>/workspace/out/` is the directory
	// the store lives in, outside every root the action declared.
	for name, target := range map[string]string{
		"out of the workspace":      "../../../../secret.txt",
		"inside it, to a real file": "../input/decoy.txt",
	} {
		t.Run(name, func(t *testing.T) {
			action := fixtureAction{
				ID:         "escape",
				Title:      "Try to publish a link",
				Executable: "helper",
				Args:       []any{helperFlag, "symlink", "{output.result}", target},
				Outputs: []map[string]any{
					{"name": "result", "title": "The written file", "role": "test.result", "path": "out/result.txt"},
				},
				Roots:   []map[string]any{{"role": "workspace", "access": "read_write", "purpose": "write the result"}},
				Timeout: 60,
			}
			h := newHarness(t, fixtureProfile(t, "test.symlink.output", action))

			secret := filepath.Join(h.dir, "secret.txt")
			if err := os.WriteFile(secret, []byte("not the tool's to publish"), 0o600); err != nil {
				t.Fatalf("writing the fixture secret: %v", err)
			}

			finished := h.runToEnd(h.helperRequest("test.symlink.output", "escape"))
			if finished.State != Failed {
				t.Fatalf("state = %s, want failed: a symlinked output must not be published", finished.State)
			}
			if !strings.Contains(finished.Error, "symbolic link") && !strings.Contains(finished.Error, "leaves the directory") {
				t.Errorf("error = %q, want it to name the link or the escape", finished.Error)
			}
			for _, artifact := range finished.Artifacts {
				if artifact.Path == "" {
					continue
				}
				if data, err := os.ReadFile(artifact.Path); err == nil && strings.Contains(string(data), "not the tool's") {
					t.Fatal("the file outside the workspace was published")
				}
			}
			// The original is untouched: refusing to publish is not the same as
			// interfering with what the link pointed at.
			if data, err := os.ReadFile(secret); err != nil || string(data) != "not the tool's to publish" {
				t.Errorf("the file outside the workspace changed: %v %q", err, data)
			}
		})
	}
}

func TestAnInputOutsideTheDeclaredRootsIsRefused(t *testing.T) {
	project := t.TempDir()
	elsewhere := t.TempDir()

	inside := filepath.Join(project, "level.map")
	outside := filepath.Join(elsewhere, "secret.map")
	for _, path := range []string{inside, outside} {
		if err := os.WriteFile(path, []byte("{ }"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	action := fixtureAction{
		ID:         "read",
		Title:      "Read a staged input",
		Executable: "helper",
		Args:       []any{helperFlag, "argv", "{input.source}"},
		Inputs: []map[string]any{
			{"name": "source", "title": "Source", "role": "test.source", "required": true, "extensions": []any{".map"}},
		},
		Roots: []map[string]any{
			{"role": "workspace", "access": "read_write", "purpose": "scratch space"},
			{"role": "project_root", "access": "read", "purpose": "read the map source"},
		},
		Timeout: 60,
	}
	h := newHarness(t, fixtureProfile(t, "test.input.roots", action))

	base := h.helperRequest("test.input.roots", "read")
	base.Roots = map[string]string{"project_root": project}

	t.Run("inside a declared root", func(t *testing.T) {
		request := base
		request.Inputs = map[string]string{"source": inside}
		finished := h.runToEnd(request)
		if finished.State != Succeeded {
			t.Fatalf("state = %s, want succeeded (error: %s)", finished.State, finished.Error)
		}
		// And what reached the program was the staged copy, inside the
		// workspace — never the user's own file.
		staged := strings.TrimSpace(h.mustLog(finished.ID, "stdout"))
		if staged == inside {
			t.Error("the program was handed the user's own file rather than a staged copy")
		}
		if content, err := os.ReadFile(inside); err != nil || string(content) != "{ }" {
			t.Errorf("the user's source file did not survive the job: %v %q", err, content)
		}
	})

	t.Run("outside every declared root", func(t *testing.T) {
		request := base
		request.Inputs = map[string]string{"source": outside}
		if _, err := h.service.Submit(request); err == nil {
			t.Fatal("an input outside the declared roots was accepted")
		}
	})

	t.Run("reached through a symlink inside a root", func(t *testing.T) {
		link := filepath.Join(project, "link.map")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("this platform cannot create symlinks: %v", err)
		}
		request := base
		request.Inputs = map[string]string{"source": link}
		_, err := h.service.Submit(request)
		if err == nil {
			t.Fatal("a symlink pointing out of the declared roots was accepted")
		}
		if !strings.Contains(err.Error(), "not inside") {
			t.Errorf("error = %v, want it to say the resolved path is outside the roots", err)
		}
	})
}

func TestTheWorkspacePathCannotBeOverriddenByARequest(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.workspace.override", writeAction()))
	elsewhere := t.TempDir()

	request := h.helperRequest("test.workspace.override", "write")
	request.Roots = map[string]string{"workspace": elsewhere}
	finished := h.runToEnd(request)

	if finished.State != Succeeded {
		t.Fatalf("state = %s, want succeeded (error: %s)", finished.State, finished.Error)
	}
	if err := within(h.store.Root(), finished.Workspace); err != nil {
		t.Errorf("the workspace was %s: %v", finished.Workspace, err)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Errorf("the request's own directory was written into: %v %v", err, entries)
	}
}

// withSessionRole is what the engine vocabulary requires of each action.
func withSessionRole(action fixtureAction, role string) fixtureAction {
	action.SessionRole = role
	return action
}
