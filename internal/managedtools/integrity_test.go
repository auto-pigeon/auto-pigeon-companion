package managedtools_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	mt "github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools/managedtoolstest"
)

// M5: every way an archive can be other than what its release says is a
// refusal; nothing is written outside the staging folder, nothing that was
// refused is run, and an installation that was there survives.
func TestEveryArchiveThatIsNotWhatItsReleaseSaysIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		spec    mt.Spec
		release func(*mt.Release)
		limits  *managedtools.Limits
		free    int64
		want    string
	}{
		{name: "a library changed after the file list was written", spec: mt.Spec{Archive: func(e map[string][]byte) map[string][]byte {
			e["bin/libfixture.so.1"] = []byte("A LIBRARY THE PROGRAMS NEED\n") // same length, other bytes
			return e
		}}, want: "SHA-256 differs"},
		{name: "a library missing", spec: mt.Spec{Archive: func(e map[string][]byte) map[string][]byte {
			delete(e, "bin/libfixture.so.1")
			return e
		}}, want: "does not contain it"},
		{name: "a program missing", spec: mt.Spec{
			ArchiveManifest: func(m map[string]any) {
				var kept []map[string]any
				for _, f := range m["files"].([]map[string]any) {
					if !strings.HasPrefix(f["path"].(string), "bin/light") {
						kept = append(kept, f)
					}
				}
				m["files"] = kept
			},
			Archive: func(e map[string][]byte) map[string][]byte {
				for name := range e {
					if strings.HasPrefix(name, "bin/light") {
						delete(e, name)
					}
				}
				return e
			}}, want: "does not contain bin/light"},
		{name: "a file the list does not name", spec: mt.Spec{Archive: func(e map[string][]byte) map[string][]byte {
			e["bin/extra"] = []byte("x")
			return e
		}}, want: "does not name"},
		{name: "a name that climbs out", spec: mt.Spec{Archive: func(e map[string][]byte) map[string][]byte {
			e["../../evil"] = []byte("escaped")
			return e
		}}, want: "escapes its root"},
		{name: "an absolute name", spec: mt.Spec{Archive: func(e map[string][]byte) map[string][]byte {
			e["!/tmp/aucom-evil"] = []byte("absolute")
			return e
		}}, want: "absolute"},
		{name: "a second top-level folder", spec: mt.Spec{Archive: func(e map[string][]byte) map[string][]byte {
			e["!elsewhere/evil"] = []byte("x")
			return e
		}}, want: "outside the archive's one top-level folder"},
		{name: "a symbolic link", spec: mt.Spec{Archive: func(e map[string][]byte) map[string][]byte {
			e["@link:bin/evil"] = []byte("../../../../etc/passwd")
			return e
		}}, want: "not a regular file"},
		{name: "two names one file on a case-insensitive disk", spec: mt.Spec{Archive: func(e map[string][]byte) map[string][]byte {
			e["README.MD"] = []byte("x")
			return e
		}}, want: "ignores case"},
		{name: "a Windows device name", spec: mt.Spec{Archive: func(e map[string][]byte) map[string][]byte {
			e["bin/CON.txt"] = []byte("x")
			return e
		}}, want: "reserved Windows device"},
		{name: "unpacks larger than the bound", limits: &managedtools.Limits{MaxFiles: 2048, MaxTotal: 1 << 10, MaxFile: 1 << 30, MaxCompressed: 1 << 30}, want: "unpacks to more than"},
		{name: "more entries than the bound", limits: &managedtools.Limits{MaxFiles: 3, MaxTotal: 1 << 30, MaxFile: 1 << 30, MaxCompressed: 1 << 30}, want: "entries"},
		{name: "a truncated download", release: func(release *mt.Release) {
			for name, body := range release.Files {
				if strings.HasSuffix(name, ".zip") {
					release.Sizes = map[string]int64{name: int64(len(body))}
					release.Digests = map[string]*string{name: mt.Digest(body)}
					release.Files[name] = body[:len(body)/2]
				}
			}
		}, want: "cut short"},
		{name: "bytes that are not the release's, with no GitHub digest", release: func(release *mt.Release) {
			for name, body := range release.Files {
				if strings.HasSuffix(name, ".zip") {
					changed := append([]byte(nil), body...)
					changed[len(changed)/2] ^= 0xff
					release.Files[name] = changed
					release.Digests = map[string]*string{name: nil}
				}
			}
		}, want: "not the file the release describes"},
		{name: "a full disk", free: 1 << 20, want: "not enough free disk space"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			// A working installation first, which must survive.
			r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4536", Prerelease: true})
			m := r.manager()
			m.Step(context.Background())
			before := tool(m.Status(), managedtools.EricW).Active
			if before == nil {
				// Fifteen parallel installs each write programs and then run
				// them; on a loaded Linux runner a fork elsewhere can hold the
				// just-written file open for a moment ("text file busy"). The
				// product's answer is the failure's one-minute backoff and a
				// second attempt, which is what this does — once.
				view := tool(m.Status(), managedtools.EricW)
				t.Logf("first install attempt failed (%+v); trying again after its backoff", view.Failure)
				r.clock.Advance(2 * time.Minute)
				m.Step(context.Background())
				before = tool(m.Status(), managedtools.EricW).Active
			}
			if before == nil {
				t.Fatalf("no first installation: %+v", tool(m.Status(), managedtools.EricW).Failure)
			}
			verified := r.host.verified

			c.spec.Tool, c.spec.Version, c.spec.Prerelease = managedtools.EricW, "1.4540", true
			release := mt.Build(t, c.spec)
			if c.release != nil {
				c.release(release)
			}
			r.server.Publish(ericwRepo, release)
			m = r.manager(func(o *managedtools.Options) {
				if c.limits != nil {
					o.Limits = *c.limits
				}
				if c.free != 0 {
					free := c.free
					o.FreeSpace = func(string) int64 { return free }
				}
			})
			r.clock.Advance(3 * 60 * 60 * 1e9)
			m.Step(context.Background())
			offer := tool(m.Status(), managedtools.EricW).Offer
			if offer == nil {
				t.Fatalf("1.4540 was not offered: %+v", tool(m.Status(), managedtools.EricW).Newer)
			}
			_ = m.Decide(managedtools.EricW, managedtools.DecideNow, offer.Identity(), false)
			m.Step(context.Background())
			view := tool(m.Status(), managedtools.EricW)
			if view.Failure == nil || !strings.Contains(view.Failure.Error, c.want) {
				t.Fatalf("failure %+v; want one saying %q", view.Failure, c.want)
			}
			if view.Active == nil || view.Active.Dir != before.Dir || view.Active.Version != "1.4536" {
				t.Fatalf("the working installation did not survive: %+v", view.Active)
			}
			if _, err := managedtools.ReadInstall(before.Dir); err != nil {
				t.Fatalf("the working installation no longer checks out: %v", err)
			}
			if r.host.verified != verified {
				t.Fatal("a refused archive's programs were run")
			}
			assertOnlyKnownInstalls(t, r.root, before.Dir)
			for _, outside := range []string{filepath.Join(filepath.Dir(r.root), "evil"), "/tmp/aucom-evil"} {
				if _, err := os.Lstat(outside); err == nil {
					t.Fatalf("%s was written", outside)
				}
			}
		})
	}
}

// assertOnlyKnownInstalls: the tools root holds the one installation and
// this program's own lock and staging names, nothing else.
func assertOnlyKnownInstalls(t *testing.T, root, keep string) {
	t.Helper()
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		parts := strings.Split(relative, string(filepath.Separator))
		switch {
		case len(parts) == 1 && (parts[0] == "ericw-tools" || parts[0] == "q3map2"):
		case strings.HasPrefix(path, keep):
		case len(parts) >= 2 && strings.HasPrefix(parts[1], "."):
			if len(parts) > 2 && parts[1] == ".staging" {
				t.Errorf("staging was left behind: %s", relative)
			}
		default:
			t.Errorf("unexpected file in the tools root: %s", relative)
		}
		return nil
	})
}

// M5: a tools folder that cannot be written is a failure that says so,
// never an installation somewhere else.
func TestAnUnwritableToolsFolderFailsAndSaysWhere(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this user cannot write")
	}
	r := newRig(t)
	if err := os.MkdirAll(r.root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(r.root, 0o700) })
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4536", Prerelease: true})
	m := r.manager()
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.EricW)
	if view.Phase != managedtools.PhaseFailed || !strings.Contains(view.Detail, r.root) {
		t.Fatalf("an unwritable root: %s %q", view.Phase, view.Detail)
	}
}

// A redirect to a host that is not one of GitHub's release hosts is refused;
// one to an allowed host is followed.
func TestDownloadsFollowOnlyRedirectsToTheReleaseHosts(t *testing.T) {
	r := newRig(t)
	r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: "1.4536", Prerelease: true})
	r.server.Mutate(func() { r.server.RedirectTo = "http://evil.invalid/{path}?X-Amz-Signature=secret" })
	m := r.manager()
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.EricW)
	if view.Active != nil || len(view.Newer) != 1 || !strings.Contains(view.Newer[0].Detail, "not one of GitHub's release hosts") {
		t.Fatalf("a redirect to another host: %+v", view)
	}
	if strings.Contains(view.Newer[0].Detail, "secret") {
		t.Fatal("a signed query string reached a message")
	}

	r.server.Mutate(func() { r.server.RedirectTo = r.server.URL + "/asset/{path}" })
	_ = m.Retry(managedtools.EricW)
	r.clock.Advance(2 * 60 * 60 * 1e9)
	m.Step(context.Background())
	if view := tool(m.Status(), managedtools.EricW); view.Phase != managedtools.PhaseReady {
		t.Fatalf("a redirect to an allowed host: %s %s", view.Phase, view.Detail)
	}
}

func TestALoopbackSourceRefusesEveryOtherHost(t *testing.T) {
	for _, base := range []string{"https://api.github.com", "http://192.168.0.33:8080", "http://example.com"} {
		if _, err := managedtools.NewLoopbackSource(base, nil); err == nil {
			t.Errorf("a loopback source accepted %s", base)
		}
	}
}
