package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/lockfile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/release"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/selfupdate"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/web"
)

// `companion selfupdate` — the Companion's own bundle (NEW_368). Offering,
// choosing and staging an update are `companion tools … companion`, the same
// coordinator as the toolchains'; this command is what is specific to
// replacing the program that is running: what kind of installation this is,
// how the last replacement ended, and the proving start a candidate runs
// before it is put in place and again after.

const selfUpdateUsage = `usage:
  companion selfupdate status [--json]       this installation, and how its last update ended
  companion selfupdate prove --expect-version <v>
                                              the proving start a new version runs before it replaces this one
  companion selfupdate recover [--json]      finish an interrupted update: put back one complete bundle
                                              (runs from the program, or from the copy kept in
                                              .companion-update/previous/ when none is left at its path)

Offers, "Update now", "Update when I close Companion" and Cancel are
` + "`companion tools status|update|skip|cancel-pending companion`" + `.
`

// SelfUpdateLogName is the compact local log of every replacement.
const SelfUpdateLogName = "self-update.log"

func runSelfUpdate(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, selfUpdateUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, selfUpdateUsage)
		return 0
	case "status":
		return selfUpdateStatus(env, args[1:])
	case "prove":
		return selfUpdateProve(env, args[1:])
	case "recover":
		return selfUpdateRecover(env, args[1:])
	}
	fmt.Fprintf(env.Stderr, "error: unknown selfupdate subcommand %q\n\n%s", args[0], selfUpdateUsage)
	return 2
}

// inspectSelf is this process's bundle.
func inspectSelf(env *Env) managedtools.SelfInstall {
	executable, err := os.Executable()
	if err != nil {
		return managedtools.SelfInstall{Version: env.Version, Mode: managedtools.SelfDevelopment, Reason: err.Error()}
	}
	return managedtools.InspectSelf(executable, env.Version, release.ReadBuild().Commit, runtime.GOOS, runtime.GOARCH)
}

func selfUpdateStatus(env *Env, args []string) int {
	set := newFlagSet(env, "selfupdate status")
	asJSON := set.Bool("json", false, "print the answer as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	self := inspectSelf(env)
	journal, journalErr := selfupdate.Read(self.Root)
	if *asJSON {
		answer := map[string]any{"installation": self, "last": journal}
		if journalErr != nil {
			answer["last_error"] = journalErr.Error()
		}
		return printJSON(env, answer)
	}
	fmt.Fprintf(env.Stdout, "Auto-Pigeon Companion %s (%s), %s\n", self.Version, self.Platform, self.Root)
	switch self.Mode {
	case managedtools.SelfPortable:
		fmt.Fprintln(env.Stdout, "  updated automatically when you choose an update: its own files are replaced, nothing else in the folder")
	case managedtools.SelfManual:
		fmt.Fprintf(env.Stdout, "  updates are downloaded and verified, and you put them in place: %s\n", self.Reason)
	default:
		fmt.Fprintf(env.Stdout, "  not updated automatically: %s\n", self.Reason)
	}
	if self.ConverterVersion != "" {
		fmt.Fprintf(env.Stdout, "  bundled converter %s\n", self.ConverterVersion)
	}
	switch {
	case journalErr != nil:
		fmt.Fprintf(env.Stdout, "  last update: unreadable (%v)\n", journalErr)
	case journal != nil:
		fmt.Fprintf(env.Stdout, "  last update: %s -> %s, %s %s", journal.From, journal.To, journal.State, journal.Updated.Local().Format(time.RFC3339))
		if journal.Detail != "" {
			fmt.Fprintf(env.Stdout, " — %s", journal.Detail)
		}
		fmt.Fprintln(env.Stdout)
	}
	return 0
}

// selfUpdateProve is a candidate's proving start. It runs from the
// candidate's own folder, so the converter it resolves and hands a protocol
// check is the one beside it, verified against the bundle manifest beside
// it; it reads this user's configuration without writing it, and serves one
// request on a loopback port of its own. It starts no job, writes no token
// and registers nothing.
func selfUpdateProve(env *Env, args []string) int {
	set := newFlagSet(env, "selfupdate prove")
	expect := set.String("expect-version", "", "the version this program must say it is")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	result := web.ProveResult{Version: env.Version, Commit: release.ReadBuild().Commit}
	finish := func(err error) int {
		if err != nil {
			result.Error = err.Error()
		}
		encoded, _ := json.Marshal(result)
		fmt.Fprintln(env.Stdout, string(encoded))
		if err != nil {
			return 1
		}
		return 0
	}
	if *expect == "" || *expect != env.Version {
		return finish(fmt.Errorf("this program is version %s, not %s", env.Version, *expect))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	runner, err := converterResolver().Resolve(ctx)
	if err != nil {
		return finish(fmt.Errorf("its converter: %w", err))
	}
	provenance := runner.Provenance()
	result.ConverterVersion, result.ConverterVerified = provenance.Version, provenance.Verified
	if !provenance.Verified {
		return finish(errors.New("its converter is not the one its bundle manifest lists"))
	}
	if self := inspectSelf(env); self.Bridge != nil {
		actual, err := managedtools.FileSHA256(filepath.Join(self.Root, filepath.FromSlash(self.Bridge.Path)))
		if err != nil || actual != self.Bridge.SHA256 {
			return finish(errors.New("the extractor this migration bridge lists is not the one beside it"))
		}
		result.ExtractorVersion, result.ExtractorVerified = self.Bridge.Version, true
	}
	if _, err := loadSettings(env); err != nil {
		return finish(fmt.Errorf("it cannot read this configuration: %w", err))
	}
	result.ConfigRead = true
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return finish(fmt.Errorf("it cannot listen on loopback: %w", err))
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ready"))
	}), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	response, err := (&http.Client{Timeout: 10 * time.Second}).Get("http://" + listener.Addr().String() + "/")
	if err != nil {
		return finish(fmt.Errorf("it did not answer on loopback: %w", err))
	}
	response.Body.Close()
	result.Served = response.StatusCode == http.StatusOK
	if !result.Served {
		return finish(fmt.Errorf("it answered HTTP %d on loopback", response.StatusCode))
	}
	return finish(nil)
}

// selfUpdateRecover is the recovery entry point. It works on the bundle the
// program it runs as belongs to — found from where that program is, never
// from an argument — so the copy an update kept in previous/ can run it when
// an interruption left no program at the Companion's own path (01_NEW_368
// §1.7). It finishes an unfinished replacement by putting the previous files
// back; a proved, activated one is a complete bundle and is left for its
// first start to confirm. It removes nothing.
func selfUpdateRecover(env *Env, args []string) int {
	set := newFlagSet(env, "selfupdate recover")
	asJSON := set.Bool("json", false, "print the answer as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	executable, err := os.Executable()
	if err != nil {
		return fail(env, err)
	}
	_, companion := managedtools.BundleLayout(managedtools.BundlePlatform(runtime.GOOS, runtime.GOARCH))
	root, err := selfupdate.RecoveryRoot(executable, companion)
	if err != nil {
		return fail(env, err)
	}
	journal, outcome, err := selfupdate.Recover(root, "", func(format string, args ...any) {
		fmt.Fprintf(env.Stderr, format+"\n", args...)
	})
	if *asJSON {
		answer := map[string]any{"root": root, "outcome": outcome, "last": journal}
		if err != nil {
			answer["error"] = err.Error()
		}
		printJSON(env, answer)
	} else {
		switch {
		case err != nil:
			fmt.Fprintf(env.Stdout, "The Companion's folder %s still needs recovery: %v\n", root, err)
		case outcome == selfupdate.RolledBack:
			fmt.Fprintf(env.Stdout, "The update %s -> %s was undone; %s is complete again in %s.\n", journal.From, journal.To, journal.From, root)
		case journal == nil:
			fmt.Fprintf(env.Stdout, "No update has been made in %s; nothing to recover.\n", root)
		default:
			fmt.Fprintf(env.Stdout, "Nothing to recover in %s: the last update %s -> %s is %s.\n", root, journal.From, journal.To, journal.State)
		}
	}
	if err != nil {
		return 1
	}
	return 0
}

// bundleCoherent reads back every file the running bundle's manifest lists.
func bundleCoherent(self managedtools.SelfInstall) error {
	for _, member := range self.Members {
		actual, err := managedtools.FileSHA256(filepath.Join(self.Root, filepath.FromSlash(member.Path)))
		if err != nil {
			return fmt.Errorf("%s: %w", member.Path, err)
		}
		if actual != member.SHA256 {
			return fmt.Errorf("%s is not the file the bundle manifest lists", member.Path)
		}
	}
	return nil
}

// selfUpdateLog is the compact log of replacements, beside config.json.
type selfUpdateLog struct {
	mu   sync.Mutex
	path string
}

func (l *selfUpdateLog) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	if info, err := file.Stat(); err == nil && info.Size() > 256<<10 {
		return
	}
	fmt.Fprintf(file, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// recoverSelfAtStart looks at the last replacement as this Companion
// starts. It returns a function to call once the process serves, which
// confirms a replacement this process is the new side of — or an error when
// the folder may hold parts of two bundles, which this process must then not
// serve (01_NEW_368 §1.1): a mixed Companion/converter/manifest bundle is
// never started as either.
func recoverSelfAtStart(self managedtools.SelfInstall, log *selfUpdateLog, record func(managedtools.SelfResult)) (func(), error) {
	if self.Mode == managedtools.SelfDevelopment {
		return func() {}, nil
	}
	sha, err := managedtools.FileSHA256(self.Executable)
	if err != nil {
		return func() {}, nil
	}
	journal, outcome, err := selfupdate.Recover(self.Root, sha, log.logf)
	if err != nil {
		log.logf("start: the last update's journal: %v", err)
	}
	switch outcome {
	case selfupdate.NeedsRecovery:
		if journal == nil {
			// The journal itself cannot be read. Whether the folder is whole
			// is then read from the files.
			if coherent := bundleCoherent(self); coherent == nil {
				log.logf("start: the last update's journal cannot be used (%v), but every file of %s is its own; serving, and refusing updates until it is recovered", err, self.Version)
				return func() {}, nil
			} else {
				err = fmt.Errorf("%v; and the bundle is not whole: %v", err, coherent)
			}
		}
		to := ""
		if journal != nil {
			to = journal.To
		}
		record(managedtools.SelfResult{From: self.Version, To: to, Outcome: "recovery_needed", Detail: err.Error(), At: time.Now().UTC()})
		return nil, fmt.Errorf("the Companion's folder %s is part way through an update and could not be put back (%v); run `%s %s`",
			self.Root, err, filepath.Base(self.Executable), selfupdate.RecoverCommand)
	case selfupdate.RolledBack:
		record(managedtools.SelfResult{From: journal.From, To: journal.To, Outcome: "rolled_back", Detail: journal.Detail, At: time.Now().UTC()})
		log.logf("start: %s -> %s rolled back: %s", journal.From, journal.To, journal.Detail)
		if sha != journal.FromSHA256 {
			// This process is the new program that was just taken out: what
			// is in the folder now is the previous one, which it is not.
			return nil, fmt.Errorf("version %s was taken back out after %d starts that never became ready; start the Companion again to run %s",
				self.Version, journal.Starts, journal.From)
		}
	case selfupdate.Pending:
		return func() {
			if confirmed, err := selfupdate.MarkVerified(self.Root, sha); err != nil {
				log.logf("start: confirming %s: %v", self.Version, err)
			} else {
				log.logf("start: %s started from %s and serves; the update from %s is complete", self.Version, self.Root, confirmed.From)
				record(managedtools.SelfResult{From: confirmed.From, To: confirmed.To, Outcome: "updated", At: time.Now().UTC()})
			}
		}, nil
	}
	return func() {}, nil
}

// relaunchArguments is how the new Companion is started after "Update now":
// the launch mode this process had, its developer switch, and the port the
// open pages are on — nothing else of this process's own arguments, and
// never --open: the pages that are open come back by themselves.
func relaunchArguments(interactive, stayRunning, debug bool, port int) []string {
	args := []string{"serve", "--managed-tools", "on"}
	switch {
	case interactive:
		args = append(args, "--interactive")
	case stayRunning:
		args = append(args, "--stay-running")
	}
	if debug {
		args = append(args, "--debug")
	}
	if port > 0 {
		args = append(args, "--port", strconv.Itoa(port))
	}
	return args
}

// relaunchWait bounds how long the old Companion waits for the new one to
// start and serve before it puts itself back.
const relaunchWait = 90 * time.Second

// replaceSelf puts a staged bundle where this one is, as this process
// ends. relaunch is "Update now": start the new one and, if it does not
// serve, put the old one back and start that instead. It returns what to do
// once every deferred close of the serve has run — starting a process
// before then would let this one's exit remove the new one's token.
func replaceSelf(self managedtools.SelfInstall, staged managedtools.InstallRecord, toolsRoot, configDir string,
	relaunch []string, log *selfUpdateLog, record func(managedtools.SelfResult)) func() {
	_, companion := managedtools.BundleLayout(self.Platform)
	// The start lock: a protocol link clicked now waits for the files to be
	// in place instead of starting a Companion halfway through.
	lock, err := lockfile.Acquire(filepath.Join(configDir, StartLockName), lockfile.Options{
		Timeout: 20 * time.Second, Program: "companion " + self.Version + " (installing its update)"})
	if err != nil {
		log.logf("exit: %s -> %s not installed: another Companion is starting (%v); it stays chosen", self.Version, staged.Version, err)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	log.logf("exit: replacing %s with %s in %s (staged %s)", self.Version, staged.Version, self.Root, staged.Dir)
	journal, err := selfupdate.Apply(ctx, selfupdate.Request{
		Root: self.Root, Companion: companion, From: self.Version, Current: self.Members,
		To: staged.Version, Staged: staged.Dir, StagedRoot: toolsRoot, Members: staged.Members,
		Prove: func(ctx context.Context, exe string) error {
			_, err := web.Prove(ctx, exe, staged.Version)
			return err
		},
		Logf:  log.logf,
		Fault: fixtureSelfUpdateFault(),
	})
	_ = lock.Release()
	if err != nil {
		outcome := "failed"
		switch {
		case errors.Is(err, selfupdate.ErrRecoveryNeeded):
			outcome = "recovery_needed"
		case errors.Is(err, selfupdate.ErrRolledBack):
			outcome = "rolled_back"
		}
		log.logf("exit: %s -> %s %s: %v", self.Version, staged.Version, outcome, err)
		record(managedtools.SelfResult{From: self.Version, To: staged.Version, Outcome: outcome, Detail: err.Error(), At: time.Now().UTC()})
		if outcome == "recovery_needed" {
			// Neither bundle is known to be whole: nothing is started from
			// the folder, the old program included (01_NEW_368 §1.3).
			log.logf("exit: nothing is restarted; run `%s %s`", filepath.Base(self.Executable), selfupdate.RecoverCommand)
			return nil
		}
		if relaunch == nil {
			return nil
		}
		// "Update now" that did not happen still restarts: the person asked
		// for the Companion back, not for it to stay closed.
		return func() { startCompanion(self.Executable, relaunch, log) }
	}
	record(managedtools.SelfResult{From: self.Version, To: staged.Version, Outcome: "activated",
		Detail: "in place and proved; confirmed when it next starts", At: time.Now().UTC()})
	log.logf("exit: %s in place (%d files); the previous files are in %s", staged.Version, len(journal.Members),
		filepath.Join(self.Root, selfupdate.DirName, "previous"))
	if relaunch == nil {
		return nil
	}
	exe := filepath.Join(self.Root, filepath.FromSlash(companion))
	return func() {
		command, err := selfupdate.Start(exe, relaunch)
		if err != nil {
			log.logf("restart: %s did not start (%v); putting %s back", staged.Version, err, self.Version)
			rollbackAndRestart(self, exe, relaunch, err.Error(), log, record)
			return
		}
		exited := make(chan struct{})
		go func() { _ = command.Wait(); close(exited) }()
		waitCtx, stop := context.WithTimeout(context.Background(), relaunchWait)
		defer stop()
		if selfupdate.WaitVerified(waitCtx, self.Root, exited) {
			log.logf("restart: %s (pid %d) serves; this process ends", staged.Version, command.Process.Pid)
			return
		}
		detail := fmt.Sprintf("version %s did not start and serve within %s", staged.Version, relaunchWait)
		select {
		case <-exited:
			detail = fmt.Sprintf("version %s exited without serving (%v)", staged.Version, command.ProcessState)
		default:
			// By the handle this process holds — never a PID looked up later.
			_ = command.Process.Kill()
			<-exited
		}
		log.logf("restart: %s; putting %s back", detail, self.Version)
		rollbackAndRestart(self, exe, relaunch, detail, log, record)
	}
}

func rollbackAndRestart(self managedtools.SelfInstall, exe string, relaunch []string, detail string,
	log *selfUpdateLog, record func(managedtools.SelfResult)) {
	journal, err := selfupdate.Rollback(self.Root, detail)
	to := ""
	if journal != nil {
		to = journal.To
	}
	if err != nil {
		// Not put back: the old program is not started as if it were
		// (01_NEW_368 §1.3), and the result says what is needed.
		log.logf("restart: rolling back: %v; nothing is restarted — run `%s %s`", err, filepath.Base(self.Executable), selfupdate.RecoverCommand)
		record(managedtools.SelfResult{From: self.Version, To: to, Outcome: "recovery_needed", Detail: detail + "; " + err.Error(), At: time.Now().UTC()})
		return
	}
	record(managedtools.SelfResult{From: self.Version, To: to, Outcome: "rolled_back", Detail: detail, At: time.Now().UTC()})
	startCompanion(exe, relaunch, log)
}

func startCompanion(exe string, args []string, log *selfUpdateLog) {
	if command, err := selfupdate.Start(exe, args); err != nil {
		log.logf("restart: %s did not start: %v", exe, err)
	} else {
		log.logf("restart: started %s (pid %d)", exe, command.Process.Pid)
		_ = command.Process.Release()
	}
}
