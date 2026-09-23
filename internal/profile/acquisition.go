package profile

import "strings"

// How a profile's executables get onto the machine.
//
// A profile declares the *choices*, in the order it recommends them. Every one
// of them is a program the person already has: the Companion downloads no
// program (operator, 2026-09-23). See internal/acquire.

// AcquisitionMode is one way to obtain a tool.
type AcquisitionMode string

const (
	// AcquireUserPath: the user points the Companion at a file they already
	// have. Nothing is downloaded and nothing is verified against a catalogue;
	// the user is the authority.
	AcquireUserPath AcquisitionMode = "user_path"
	// AcquireSystemPath: found on PATH, by one of the names declared here.
	AcquireSystemPath AcquisitionMode = "system_path"
	// AcquireManagedDownload is LEGACY: a route through the signed catalogue
	// this Companion no longer has. A document that still lists it is read —
	// published documents carry it — and the route is never offered or taken;
	// internal/acquire answers it with ErrNoDownloads.
	AcquireManagedDownload AcquisitionMode = "managed_download"
	// AcquireAlreadyInstalled: shipped with a game or another package, found
	// at a known place relative to a root the user has already configured.
	AcquireAlreadyInstalled AcquisitionMode = "already_installed"
)

var acquisitionModes = []string{
	string(AcquireUserPath), string(AcquireSystemPath),
	string(AcquireManagedDownload), string(AcquireAlreadyInstalled),
}

// AcquisitionOption is one declared route to the executables.
type AcquisitionOption struct {
	Mode  AcquisitionMode `json:"mode" aucom:"required"`
	Title string          `json:"title" aucom:"required"`
	// Platforms restricts the option. Empty means all of the profile's.
	Platforms []Platform `json:"platforms,omitempty"`
	// CatalogPackage is LEGACY, with `managed_download`: the catalogue entry an
	// older document named. Read and checked, never looked up.
	CatalogPackage string `json:"catalog_package,omitempty"`
	// Commands are the executable names to look for on PATH, for
	// `system_path`.
	Commands []string `json:"commands,omitempty"`
	// RelativeTo and Path locate the install under an existing root, for
	// `already_installed`.
	RelativeTo string `json:"relative_to,omitempty"`
	Path       string `json:"path,omitempty"`
	// Hint tells the user what to point at, for `user_path`.
	Hint string `json:"hint,omitempty"`
	// Note is anything else the user should know before choosing this route.
	Note string `json:"note,omitempty"`
}

func (a AcquisitionOption) validate(c *collector) {
	c.child(field("mode"), func(c *collector) {
		if !contains(acquisitionModes, string(a.Mode)) {
			c.fixf("use one of: "+strings.Join(acquisitionModes, ", "), "is %q", a.Mode)
		}
	})
	c.child(field("title"), func(c *collector) { checkText(c, a.Title, maxNameLength, true) })
	c.child(field("platforms"), func(c *collector) {
		for i, p := range a.Platforms {
			c.child(index(i), p.validate)
		}
	})
	c.child(field("catalog_package"), func(c *collector) {
		switch {
		case a.Mode == AcquireManagedDownload && a.CatalogPackage == "":
			c.fixf("name the catalogue entry to look up", "is empty for a managed download")
		case a.Mode != AcquireManagedDownload && a.CatalogPackage != "":
			c.fixf("remove it", "names a catalogue entry for a %q acquisition", a.Mode)
		case a.CatalogPackage != "":
			checkID(c, a.CatalogPackage)
		}
	})
	c.child(field("commands"), func(c *collector) {
		switch {
		case a.Mode == AcquireSystemPath && len(a.Commands) == 0:
			c.fixf("list the command names to look for", "is empty for a system-path acquisition")
		case a.Mode != AcquireSystemPath && len(a.Commands) > 0:
			c.fixf("remove them", "lists commands for a %q acquisition", a.Mode)
		}
		for i, cmd := range a.Commands {
			c.child(index(i), func(c *collector) {
				checkText(c, cmd, 64, true)
				checkNoShellSyntax(c, cmd)
				if strings.ContainsAny(cmd, `/\`) {
					c.fixf("a PATH lookup takes a bare command name; use `already_installed` for a path",
						"contains a path separator")
				}
			})
		}
	})
	c.child(field("relative_to"), func(c *collector) {
		switch {
		case a.Mode == AcquireAlreadyInstalled && a.RelativeTo == "":
			c.fixf("name the root the install sits under", "is empty for an already-installed acquisition")
		case a.RelativeTo != "" && !contains(rootRoles, a.RelativeTo):
			c.fixf("use one of: "+strings.Join(rootRoles, ", "), "is %q, which is not a known root role", a.RelativeTo)
		}
	})
	c.child(field("path"), func(c *collector) { checkRelativePath(c, a.Path, false) })
	c.child(field("hint"), func(c *collector) {
		checkText(c, a.Hint, maxSummaryLength, false)
		if a.Mode == AcquireUserPath && strings.TrimSpace(a.Hint) == "" {
			c.fixf("say what file the user should choose", "is empty, and a user-path acquisition must say what to point at")
		}
	})
	c.child(field("note"), func(c *collector) { checkText(c, a.Note, maxTextLength, false) })
}

// permission returns the permission this acquisition route asks for. None
// does: every route uses something the user already has, and the legacy
// managed download is never taken.
func (a AcquisitionOption) permission() (string, Risk, string, bool) {
	return "", "", "", false
}

// VersionProbe is how the Companion asks an installed tool what version it is.
//
// It exists because a binding records a version and a binding can be wrong: a
// user updates a tool in place, or points at a different build, and the
// recorded version is then a claim nobody checked. Asking the program is the
// only answer that is true at the moment it matters.
type VersionProbe struct {
	// Executable names which of the profile's executables to ask.
	Executable string `json:"executable" aucom:"required"`
	// Args is what to pass it, usually `["--version"]`.
	Args []Arg `json:"args,omitempty"`
	// Parse says where in the output the version is.
	Parse VersionParse `json:"parse" aucom:"required"`
	// TimeoutSeconds bounds the probe. A version probe that hangs is a tool
	// waiting for input; ten seconds is generous.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// VersionParse is how to read a version out of a program's output. Literal
// rules only, for the same reason diagnostics have no regular expressions.
type VersionParse struct {
	// Kind is `first_line`, `line_containing` or `after_marker`.
	Kind string `json:"kind" aucom:"required"`
	// Marker is the literal text, for the two kinds that need one.
	Marker string `json:"marker,omitempty"`
	// Stream is `stdout` (the default) or `stderr`; several tools print their
	// version to stderr and a probe that only reads stdout finds nothing.
	Stream string `json:"stream,omitempty"`
}

var versionParseKinds = []string{"first_line", "line_containing", "after_marker"}

func (v VersionProbe) validate(c *collector, executables map[string]bool) {
	c.child(field("executable"), func(c *collector) {
		if v.Executable == "" {
			c.fixf("name one of the profile's executables", "is required and empty")
			return
		}
		if !executables[v.Executable] {
			c.fixf("declared executables are: "+strings.Join(sortedKeys(executables), ", "),
				"names the undeclared executable %q", v.Executable)
		}
	})
	c.child(field("args"), func(c *collector) {
		s := scope{platform: true}
		for i, arg := range v.Args {
			c.child(index(i), func(c *collector) { arg.validate(c, s) })
		}
	})
	c.child(field("parse"), func(c *collector) {
		c.child(field("kind"), func(c *collector) {
			if !contains(versionParseKinds, v.Parse.Kind) {
				c.fixf("use one of: "+strings.Join(versionParseKinds, ", "), "is %q", v.Parse.Kind)
			}
		})
		c.child(field("marker"), func(c *collector) {
			checkText(c, v.Parse.Marker, 128, false)
			if v.Parse.Kind != "first_line" && strings.TrimSpace(v.Parse.Marker) == "" {
				c.fixf("give the literal text to look for", "is empty, and %q needs a marker", v.Parse.Kind)
			}
		})
		c.child(field("stream"), func(c *collector) {
			if v.Parse.Stream != "" && !contains([]string{"stdout", "stderr"}, v.Parse.Stream) {
				c.fixf("use `stdout` or `stderr`", "is %q", v.Parse.Stream)
			}
		})
	})
	c.child(field("timeout_seconds"), func(c *collector) {
		if v.TimeoutSeconds < 0 || v.TimeoutSeconds > 300 {
			c.addf("is %d; a version probe's timeout must be between 0 and 300 seconds", v.TimeoutSeconds)
		}
	})
}

// Extract pulls the version string out of captured output.
func (v VersionProbe) Extract(output string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	switch v.Parse.Kind {
	case "first_line":
		for _, line := range lines {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				return trimmed, true
			}
		}
	case "line_containing":
		for _, line := range lines {
			if strings.Contains(line, v.Parse.Marker) {
				return strings.TrimSpace(line), true
			}
		}
	case "after_marker":
		for _, line := range lines {
			if _, after, found := strings.Cut(line, v.Parse.Marker); found {
				if fields := strings.Fields(after); len(fields) > 0 {
					return fields[0], true
				}
			}
		}
	}
	return "", false
}
