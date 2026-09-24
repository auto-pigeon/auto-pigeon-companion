package build

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Build roots: directories a build reads, as opposed to files it stages.
//
// # Why this is not an input
//
// An input is a FILE. The runner copies each one into the build directory
// because `vis` and `light` rewrite what they are handed, and a build that let a
// tool reach the user's own BSP could damage it. That model is right for a map
// source and wrong for a texture collection: a Quake 1 map reads the WADs its
// worldspawn declares, `qbsp` is pointed at the DIRECTORY holding them with
// `-wadpath`, and there is no single file to stage.
//
// `AUCOM/AUT 246I` classified the texture input as a folder in the UI and left
// the data model alone, so the page offered a folder chooser to a request
// pipeline that ran every value through `checkOpenFile` and rejected a directory
// as "not a file". `AUCOM/AUE/AUT 246I1` fixes the model: a directory is a ROOT,
// roots travel beside the inputs, and the one thing that consumes them is the
// same [profile.Resolve] every other root goes through.
//
// # Why a build may not set every root
//
// The executor already merges a request's roots over the machine's persistent
// bindings, which is how a user points at a copy of a tool they already have.
// A BUILD's roots are narrower than that on purpose:
//
//   - `workspace` is created and destroyed per job by internal/job, and a
//     request that could move it could point it at the user's home directory;
//   - `build_root` is the runner's own, the place where one pipeline's stages
//     hand files to each other, and a build that could aim it elsewhere could
//     make one step read another build's intermediates;
//   - `tool_root` is where a tool's executables are, and it is resolved from
//     the binding a user granted. A per-build override of it is a per-build
//     override of WHICH PROGRAM RUNS, which is not a thing a map's textures get
//     to decide.
//
// Everything else — `content_root`, `project_root`, `game_root`, `tool_cache` —
// is a place on this machine that the action must have DECLARED before a build
// may supply it, with the access the declaration gives.
//
// # And why it does not touch the binding
//
// A root supplied here lives for one build. The persistent binding is what the
// user configured; overwriting it so that one map's WAD directory became the
// EricW profile's permanent `content_root` would silently change every later
// build, which is the defect `246I1` names in its own words.

// protectedRoots are the roles a build request may never supply.
var protectedRoots = []string{profile.RootWorkspace, profile.RootBuild, profile.RootToolInstall}

// RootSourceKind says where a supplied root's contents came from.
type RootSourceKind string

const (
	// RootFromTextureBundle: a verified AUB map texture export.
	RootFromTextureBundle RootSourceKind = "aub_texture_bundle"
	// RootFromLocalDirectory: a directory the user chose on this machine.
	RootFromLocalDirectory RootSourceKind = "local_directory"
)

// RootSource is where a supplied root came from, for the manifest.
//
// The LOCAL PATH is not in here. It is recorded separately as diagnostic data,
// because a path is a fact about one machine and this is the part that travels:
// which bundle, at which revision, with which digest.
type RootSource struct {
	Kind RootSourceKind `json:"kind"`

	// Bundle identifies an AUB texture export exactly. Nil for a local
	// directory, whose contents this program has no identity for and does not
	// invent one.
	Bundle *BundleRef `json:"bundle,omitempty"`

	// OwnFiles are WADs the person supplied from their own copy to complete a
	// bundle AUB could not carry them in (Build & Run's "use my own copy").
	// Recorded beside the bundle, whose own compiler_ready stays AUB's verdict,
	// so a manifest says exactly what completed it.
	OwnFiles []BundleFile `json:"own_files,omitempty"`
}

// BundleRef identifies a verified AUB texture export.
type BundleRef struct {
	// Schema is the bundle manifest's own version.
	Schema string `json:"schema"`
	// Backend is the AUB instance, as an address. A fact about where the bytes
	// came from, and — like [SourceRef.Backend] — deliberately not part of the
	// reproducible key.
	Backend string `json:"backend,omitempty"`

	MapID    string `json:"map_id"`
	Revision int    `json:"revision"`

	// Digest is the SHA-256 of the bundle as it arrived: this bundle's
	// immutable identity, and what a later build compares.
	Digest string `json:"digest"`

	// WADsDeclared is the map's declaration order. It is precedence, not
	// decoration, so it is part of the recipe.
	WADsDeclared []string `json:"wads_declared,omitempty"`

	// Files are the carried members, each with the digest it was verified at.
	Files []BundleFile `json:"files,omitempty"`

	// CompilerReady is AUB's verdict, recorded so a manifest says whether the
	// build had everything it needed rather than leaving a reader to infer it.
	CompilerReady bool `json:"compiler_ready"`
	// CompilerRefusals is why not. Empty exactly when CompilerReady is true.
	CompilerRefusals []string `json:"compiler_refusals,omitempty"`
}

// BundleFile is one carried file of a texture bundle.
type BundleFile struct {
	Path   string `json:"path"`
	Source string `json:"source,omitempty"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// RootRecord is one supplied root, as the manifest records it.
type RootRecord struct {
	Role string `json:"role"`
	// Path is where it was on this machine. Diagnostic only: it is not
	// portable, and it is excluded from the reproducible key for the same
	// reason every other absolute path is.
	Path string `json:"path,omitempty"`
	// Access is what the resolved action declared it needs.
	Access profile.Access `json:"access,omitempty"`

	Source *RootSource `json:"source,omitempty"`
}

// checkRoots validates the request's roots against the pipeline's resolved
// steps, and returns them in a stable order.
//
// Every rule the contract states is checked here, once, before anything is
// downloaded or staged — a build that would be refused for supplying an
// undeclared root should be refused before it has compiled two stages.
func checkRoots(request Request, steps []profile.ResolvedStep) ([]RootRecord, error) {
	if len(request.Roots) == 0 {
		return nil, nil
	}
	// Which roles the actions in this pipeline declare, and with what access.
	// A role two actions declare differently takes the STRICTER reading: if any
	// declaring action asks only to read it, that is what the manifest says it
	// was given, and nothing here widens it.
	declared := map[string]profile.Access{}
	for _, step := range steps {
		for _, root := range step.Action.Roots {
			previous, seen := declared[root.Role]
			if !seen || previous == profile.AccessReadWrite {
				declared[root.Role] = root.Access
			}
		}
	}

	records := make([]RootRecord, 0, len(request.Roots))
	for _, role := range sortedKeys(request.Roots) {
		path := strings.TrimSpace(request.Roots[role])
		switch {
		case path == "":
			return nil, fmt.Errorf("the %q root was supplied with no path", role)
		case containsString(protectedRoots, role):
			return nil, fmt.Errorf(
				"the %q root is the build's own and cannot be supplied by a request; it is %s",
				role, protectedRootReason(role))
		case !containsString(profile.RootRoles(), role):
			return nil, fmt.Errorf("%q is not a root role; the roles are: %s",
				role, strings.Join(profile.RootRoles(), ", "))
		}
		access, isDeclared := declared[role]
		if !isDeclared {
			return nil, fmt.Errorf(
				"the %q root was supplied and no step of this pipeline declares it; "+
					"a root a tool never asked for is a directory nothing would read",
				role)
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("the %q root: %w", role, err)
		}
		info, err := os.Stat(absolute)
		switch {
		case err != nil:
			return nil, fmt.Errorf("the %q root: %w", role, err)
		case !info.IsDir():
			return nil, fmt.Errorf(
				"the %q root is %s, which is a file; a root is a directory, and a file is an input",
				role, absolute)
		}
		record := RootRecord{Role: role, Path: absolute, Access: access}
		if source, named := request.RootSources[role]; named {
			provenance := source
			record.Source = &provenance
		}
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].Role < records[j].Role })

	return records, nil
}

// rootPaths is the resolved role→path map the executor is handed.
func rootPaths(records []RootRecord) map[string]string {
	if len(records) == 0 {
		return nil
	}
	out := make(map[string]string, len(records))
	for _, record := range records {
		out[record.Role] = record.Path
	}

	return out
}

func protectedRootReason(role string) string {
	switch role {
	case profile.RootWorkspace:
		return "created and removed by the executor for each job"
	case profile.RootBuild:
		return "where this pipeline's stages hand files to each other"
	default:
		return "where the tool this build runs is installed, which comes from the binding you granted"
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}
