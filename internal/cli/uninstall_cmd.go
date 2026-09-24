package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/urischeme"
)

// `companion uninstall` — remove what this program put on this machine, for
// this user.
//
// # Why it exists at all, and why a package script is not enough
//
// A .deb, an .rpm and an Inno Setup uninstaller all run as somebody who is not
// necessarily the person whose files are at stake. On Linux they run as root,
// on a shared machine there may be several users, and none of them said
// anything about deleting their build history. So the packages remove the
// program and nothing else, and this is the command the person whose files they
// are runs themselves.
//
// # What it will and will not delete
//
// It deletes only directories THIS PROGRAM CREATED, and it decides that by
// name: a directory whose last element is `auto-pigeon-companion`. If somebody
// pointed AUCOM_JOBS_DIR at `~/projects`, purging must not delete `~/projects`,
// and the honest thing is to say so rather than to guess. Such a directory is
// listed as "not removed" with the reason.
//
// It never deletes the executable. Whatever installed that owns it, and a
// program that deleted itself would leave a package manager believing something
// is there.
//
// # Nothing happens without --confirm
//
// The default is a listing: every directory, its size, and what is in it. That
// is the whole command for most people, because "where does this keep things"
// is the question they actually have.

const uninstallUsage = `usage:
  companion uninstall                what this program has put on this machine
  companion uninstall --purge --confirm
                                     delete it

--purge removes this user's configuration, cache and build history. It does NOT
remove the program: whatever installed that owns it.

What is deleted holds decisions as well as content — granted profiles and their
bindings — so removing it and reinstalling starts a genuinely fresh machine, not
the same one with a cleared cache.
`

// removable is one directory the uninstaller considered.
type removable struct {
	Label   string
	Path    string
	Holds   string
	Present bool
	Bytes   int64
	Files   int
	// Refused is why this directory is listed but not removed.
	Refused string
}

func runUninstall(env *Env, args []string) int {
	set := newFlagSet(env, "uninstall")
	purge := set.Bool("purge", false, "delete this user's configuration, cache and build history")
	confirm := set.Bool("confirm", false, "actually delete, having read the list")
	asJSON := set.Bool("json", false, "print the listing as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	targets, err := uninstallTargets(env)
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, targets)
	}

	printUninstallTargets(env, targets)

	if !*purge {
		fmt.Fprint(env.Stdout, "\nNothing has been deleted. `companion uninstall --purge --confirm` deletes what is listed above.\n")
		return 0
	}
	if !*confirm {
		fmt.Fprint(env.Stderr, "\nerror: --purge deletes the directories above. Add --confirm once you have read them.\n")
		return 2
	}

	// The handler goes first: a scheme pointing at a program whose data has
	// been removed is the state that produces a silent failure later.
	registrar := env.uriRegistrar()
	state, err := registrar.Unregister()
	switch {
	case errors.Is(err, urischeme.ErrNotPerformable):
		fmt.Fprintf(env.Stdout, "\n%s\n", state.Detail)
	case err != nil:
		fmt.Fprintf(env.Stderr, "\nwarning: the %s:// handler could not be removed: %v\n", urischeme.Scheme, err)
	default:
		fmt.Fprintf(env.Stdout, "\nremoved the %s:// handler\n", urischeme.Scheme)
	}

	removed, failed := 0, 0
	for _, target := range targets {
		if !target.Present || target.Refused != "" {
			continue
		}
		if err := os.RemoveAll(target.Path); err != nil {
			fmt.Fprintf(env.Stderr, "error: removing %s: %v\n", target.Path, err)
			failed++
			continue
		}
		fmt.Fprintf(env.Stdout, "removed %s\n", target.Path)
		removed++
	}
	fmt.Fprintf(env.Stdout, "\n%d directories removed", removed)
	if failed > 0 {
		fmt.Fprintf(env.Stdout, ", %d could not be", failed)
	}
	fmt.Fprintln(env.Stdout, ". The program itself was not touched.")
	if failed > 0 {
		return 1
	}
	return 0
}

// uninstallTargets resolves every directory this program keeps state in.
func uninstallTargets(env *Env) ([]removable, error) {
	settings, err := loadSettings(env)
	if err != nil {
		return nil, err
	}

	configDir := ""
	if env.ConfigPath != "" {
		configDir = filepath.Dir(env.ConfigPath)
	} else if dir, err := config.Dir(); err == nil {
		configDir = dir
	}

	targets := []removable{{
		Label: "configuration",
		Path:  configDir,
		Holds: "config.json and the AUB session, bindings and grants, imported profiles " +
			"(and, from an older version, its catalogue trust state and licence acknowledgements)",
	}}
	for _, resolve := range []struct {
		label, holds string
		fn           func() (string, error)
	}{
		{"build history", "job records, logs and published artifacts", settings.Jobs},
		{"asset cache", "maps and other assets synced from auto-pigeon-backend", settings.AssetCache},
	} {
		path, err := resolve.fn()
		if err != nil {
			continue // A machine with no resolvable cache directory simply has none.
		}
		targets = append(targets, removable{Label: resolve.label, Path: path, Holds: resolve.holds})
	}

	seen := map[string]bool{}
	out := make([]removable, 0, len(targets))
	for _, target := range targets {
		if target.Path == "" || seen[target.Path] {
			continue
		}
		seen[target.Path] = true
		target.Refused = refuseToRemove(target.Path)
		if info, err := os.Stat(target.Path); err == nil && info.IsDir() {
			target.Present = true
			target.Bytes, target.Files = measure(target.Path)
		}
		out = append(out, target)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

// refuseToRemove reports why a directory must not be deleted, or "".
//
// The rule is by NAME, and deliberately blunt: a directory whose last element
// is not this program's own is a directory somebody pointed this program at,
// and the difference between a cache and `~/projects` is not something a
// program can work out from the outside.
func refuseToRemove(path string) string {
	clean := filepath.Clean(path)
	if base := filepath.Base(clean); base == config.AppDirName {
		return ""
	}
	// A directory INSIDE one of ours — the default jobs and tools directories
	// are — is ours too.
	if strings.Contains(clean, string(filepath.Separator)+config.AppDirName+string(filepath.Separator)) {
		return ""
	}
	return "this directory was configured rather than created by this program, and its name is not " +
		config.AppDirName + ". Remove it yourself if you want it gone"
}

// measure totals a directory, for a listing a person decides from.
func measure(root string) (int64, int) {
	var bytes int64
	var files int
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil // An unreadable corner makes the total approximate, not absent.
		}
		if entry.IsDir() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			bytes += info.Size()
			files++
		}
		return nil
	})
	return bytes, files
}

func printUninstallTargets(env *Env, targets []removable) {
	fmt.Fprintln(env.Stdout, "This user's Auto-Pigeon Companion state:")
	for _, target := range targets {
		fmt.Fprintf(env.Stdout, "\n  %s\n    %s\n", target.Label, target.Path)
		fmt.Fprintf(env.Stdout, "    holds: %s\n", wrapAt(target.Holds, 11))
		switch {
		case !target.Present:
			fmt.Fprintln(env.Stdout, "    not there")
		default:
			fmt.Fprintf(env.Stdout, "    %d files, %s\n", target.Files, humanBytes(target.Bytes))
		}
		if target.Refused != "" {
			fmt.Fprintf(env.Stdout, "    NOT REMOVED: %s\n", wrapAt(target.Refused, 17))
		}
	}
	fmt.Fprintln(env.Stdout, "\nThe program itself is never removed by this command: whatever installed it owns it.")
}

func humanBytes(size int64) string {
	switch {
	case size >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(size)/float64(1<<30))
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/float64(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(size)/float64(1<<10))
	}
	return fmt.Sprintf("%d bytes", size)
}
