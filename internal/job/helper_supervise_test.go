package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// A Companion in a process of its own, for the recovery fixtures (NEW_326).
//
// Recovery is about a SECOND process reading what a first one left: a
// supervisor that crashed, or one that is alive while somebody copies its
// state. Neither can be staged inside the test process — it cannot crash
// itself, and a supervisor that is the test process is "alive" whatever the
// fixture wants. So the test binary re-executes itself as a supervisor: the
// production NewService and Start over `<root>/jobs`, the fixture catalog in
// `<root>/profiles`, every permission granted, one job submitted. It prints
// `job <id>` once the job is recorded, `done <state>` when it ends, and then
// stays alive, as a running Companion does, until the test stops it by the
// handle it holds.

// superviseMain is `supervise <root> <profile id> <action id>`.
func superviseMain(args []string) int {
	if len(args) != 3 {
		fmt.Fprintln(os.Stderr, "supervise: want <root> <profile> <action>")
		return 2
	}
	root, profileID, actionID := args[0], args[1], args[2]
	store, err := OpenStore(filepath.Join(root, "jobs"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	catalog := NewCatalog(filepath.Join(root, "profiles"))
	entries, err := catalog.readDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	grants := map[string]binding.LocalBinding{}
	for _, entry := range entries {
		meta := entry.Profile.Metadata()
		grants[meta.ID] = binding.LocalBinding{
			SchemaVersion: binding.SchemaVersion, ProfileID: meta.ID, ProfileDigest: entry.Digest, Trust: entry.Trust,
			Grant: &profile.Grant{ProfileID: meta.ID, Version: meta.Version, Digest: entry.Digest, Trust: entry.Trust,
				Granted: profile.PermissionIDs(entry.Profile), GrantedAt: time.Now().UTC()},
		}
	}
	service, err := NewService(Options{
		Store: store, Catalog: catalog, Concurrency: 1,
		Bindings: func(id string) (binding.LocalBinding, bool) { local, found := grants[id]; return local, found },
		Logf:     func(format string, a ...any) { fmt.Fprintf(os.Stderr, "supervisor: "+format+"\n", a...) },
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := service.Start(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	submitted, err := service.Submit(Request{ProfileID: profileID, ActionID: actionID,
		Executables: map[string]string{"helper": os.Args[0]}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("job %s\n", submitted.ID)
	_ = os.Stdout.Sync()
	finished, err := service.Wait(context.Background(), submitted.ID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("done %s\n", finished.State)
	_ = os.Stdout.Sync()
	select {} // A Companion keeps running after its build; the test stops it.
}

// waitForFile polls for a file in the working directory. A barrier the test
// opens by writing one; never a guessed sleep.
func waitForFile(name string) {
	for {
		if _, err := os.Stat(name); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
