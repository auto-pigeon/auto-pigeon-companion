package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// toolchainHomepage sets or clears the homepage of one of this machine's own
// toolchains — the command-line half of the Homepage field on its page, and
// the same edit ([profile.WithHomepage]): `source.homepage`, patch version
// bumped, approval to be given again.
func toolchainHomepage(env *Env, args []string) int {
	set := newFlagSet(env, "toolchain homepage")
	clear := set.Bool("clear", false, "remove the homepage instead of setting one")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if (len(rest) != 2 && !*clear) || (len(rest) != 1 && *clear) {
		fmt.Fprintln(env.Stderr, "error: toolchain homepage takes a toolchain id and a URL, or an id and --clear")
		return 2
	}
	homepage := ""
	if !*clear {
		homepage = rest[1]
	}

	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	_, profilesDir, _, err := statePaths(env, settings)
	if err != nil {
		return fail(env, err)
	}
	entry, err := job.NewCatalog(profilesDir).Lookup(rest[0])
	if err != nil {
		return fail(env, err)
	}
	within, err := filepath.Rel(profilesDir, entry.Source)
	if err != nil || strings.HasPrefix(within, "..") || filepath.IsAbs(within) {
		fmt.Fprintf(env.Stderr, "error: %s shipped with the Companion, so its document cannot be edited; "+
			"make your own toolchain from it (New profile) and give that one a homepage\n", rest[0])
		return 1
	}
	if _, statErr := os.Stat(entry.Source); statErr != nil {
		return fail(env, statErr)
	}

	edited, err := profile.WithHomepage(entry.Profile, homepage)
	if errors.Is(err, profile.ErrHomepageUnchanged) {
		fmt.Fprintf(env.Stdout, "%s: the homepage is already that; nothing changed\n", rest[0])
		return 0
	}
	if err != nil {
		return fail(env, err)
	}
	if err := profile.WriteCanonical(entry.Source, edited); err != nil {
		return fail(env, err)
	}
	digest, err := profile.Digest(edited)
	if err != nil {
		return fail(env, err)
	}
	meta := edited.Metadata()
	if homepage == "" {
		fmt.Fprintf(env.Stdout, "%s %s → %s: homepage removed\n", meta.ID, entry.Profile.Metadata().Version, meta.Version)
	} else {
		fmt.Fprintf(env.Stdout, "%s %s → %s: homepage %s\n", meta.ID, entry.Profile.Metadata().Version, meta.Version, homepage)
	}
	fmt.Fprintf(env.Stdout, "The document changed, so it has to be approved again before it runs:\n"+
		"  companion toolchain review %s\n  companion toolchain grant %s --digest=%s --approve\n", meta.ID, meta.ID, digest)
	return 0
}
