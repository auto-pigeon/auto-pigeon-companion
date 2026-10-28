package managedtoolstest_test

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	mt "github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools/managedtoolstest"
)

// TestServeFixtureReleasesForALiveTask is not a test of anything: it is the
// loopback stand-in for GitHub that a task's live, controlled update journey
// points a FIXTURE build at (`-tags aucom_fixture_releases`,
// AUCOM_FIXTURE_RELEASES_URL). It runs only when AUCOM_FIXTURE_RELEASES_DIR
// names a folder, writes the address to url.txt there, and stops when
// stop.txt appears or after AUCOM_FIXTURE_RELEASES_MINUTES (default 60).
//
//	POST /control/publish?tool=ericw-tools&version=1.4540[&prints=1.4539]
//	POST /control/publish-companion?version=1.901&companion=<file>&extractor=<file>&extractor_version=1.245
//	POST /control/hold     archive downloads wait until /control/release
//	POST /control/release
func TestServeFixtureReleasesForALiveTask(t *testing.T) {
	dir := os.Getenv("AUCOM_FIXTURE_RELEASES_DIR")
	if dir == "" {
		t.Skip("only for a live task's fixture transport")
	}
	server := mt.NewServer(t)
	repo := map[string]string{"ericw-tools": "auto-pigeon/ericw-tools", "q3map2": "auto-pigeon/q3map2"}
	publish := func(tool, version, prints string) {
		server.Publish(repo[tool], mt.Build(t, mt.Spec{Tool: managedtools.ToolID(tool), Version: version,
			Prerelease: true, PrintsVersion: prints}))
		t.Logf("published %s %s (prints %q)", tool, version, prints)
	}
	publish("ericw-tools", "1.4536", "")
	publish("q3map2", "2.5.17n-ap.3031", "")
	control := http.NewServeMux()
	control.HandleFunc("POST /control/publish", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		publish(query.Get("tool"), query.Get("version"), query.Get("prints"))
	})
	// A Companion release built from real programs on disk (NEW_368): the
	// archive and both manifests in the release's own formats.
	control.HandleFunc("POST /control/publish-companion", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		companion, err := os.ReadFile(query.Get("companion"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		extractor, err := os.ReadFile(query.Get("extractor"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		server.Publish(mt.CompanionRepo, mt.BuildCompanion(t, mt.CompanionSpec{Version: query.Get("version"), Prerelease: true,
			Companion: companion, Extractor: extractor, ExtractorVersion: query.Get("extractor_version")}))
		t.Logf("published companion %s (%s, %d bytes)", query.Get("version"), query.Get("companion"), len(companion))
	})
	var hold chan struct{}
	control.HandleFunc("POST /control/hold", func(w http.ResponseWriter, r *http.Request) {
		hold = make(chan struct{})
		server.Mutate(func() { server.Hold = hold })
	})
	control.HandleFunc("POST /control/release", func(w http.ResponseWriter, r *http.Request) {
		server.Mutate(func() { server.Hold = nil })
		if hold != nil {
			close(hold)
			hold = nil
		}
	})
	controlServer := &http.Server{Addr: "127.0.0.1:0", Handler: control}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go controlServer.Serve(listener)
	defer controlServer.Close()
	_ = os.WriteFile(filepath.Join(dir, "url.txt"), []byte(server.URL+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "control.txt"), []byte("http://"+listener.Addr().String()+"\n"), 0o644)
	minutes := 60
	if value := os.Getenv("AUCOM_FIXTURE_RELEASES_MINUTES"); value != "" {
		if parsed, err := time.ParseDuration(value + "m"); err == nil {
			minutes = int(parsed.Minutes())
		}
	}
	deadline := time.Now().Add(time.Duration(minutes) * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "stop.txt")); err == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}
