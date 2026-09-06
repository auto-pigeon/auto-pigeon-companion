package profile

import "strings"

// ToolProfile describes one external program, or one family of programs shipped
// together, that the Companion can run on a user's behalf.
//
// The programs are separate processes under their own licences. Nothing here
// links them, vendors them or relicenses them; a profile is configuration for
// somebody else's software, and the licence member says whose.
type ToolProfile struct {
	Meta
	// GameProfile is the AUB Game Profile this tool applies to, when it applies
	// to one. A tool that operates on a file format rather than on a project —
	// an archive packer, an image converter — leaves it empty.
	GameProfile *GameProfileRef `json:"game_profile,omitempty"`
	// ToolVersion is the *upstream program's* version, which is not this
	// document's version. Both change, for different reasons, and conflating
	// them is how a documentation fix looks like a compiler upgrade.
	ToolVersion string `json:"tool_version" aucom:"required"`
	// Platforms is what is claimed about each target, including the negative
	// claims.
	Platforms []PlatformSupport `json:"platforms" aucom:"required"`
	// Acquisition is how to obtain the program, best route first.
	Acquisition []AcquisitionOption `json:"acquisition" aucom:"required"`
	// VersionProbe asks an installed copy what it actually is.
	VersionProbe *VersionProbe `json:"version_probe,omitempty"`
	// Capabilities is what this tool can do, in terms a pipeline can name.
	Capabilities []Capability `json:"capabilities,omitempty"`
	Executables  []Executable `json:"executables" aucom:"required"`
	Actions      []Action     `json:"actions" aucom:"required"`
}

// Metadata implements [Profile].
func (t *ToolProfile) Metadata() Meta { return t.Meta }

// ActionList implements [Profile].
func (t *ToolProfile) ActionList() []Action { return t.Actions }

// ActionByID implements [Profile].
func (t *ToolProfile) ActionByID(id string) (Action, bool) { return findAction(t.Actions, id) }

func findAction(actions []Action, id string) (Action, bool) {
	for _, a := range actions {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}

func executableSet(list []Executable) map[string]bool {
	set := make(map[string]bool, len(list))
	for _, e := range list {
		set[e.Name] = true
	}
	return set
}

func capabilitySet(list []Capability) map[string]bool {
	set := make(map[string]bool, len(list))
	for _, cap := range list {
		set[cap.ID] = true
	}
	return set
}

// Validate implements [Profile]. It reports every fault it finds, not the
// first.
func (t *ToolProfile) Validate() error {
	c := root("")
	t.Meta.validate(c, KindTool)
	if t.GameProfile != nil {
		c.child(field("game_profile"), t.GameProfile.validate)
	}
	c.child(field("tool_version"), func(c *collector) {
		checkText(c, t.ToolVersion, 64, true)
		checkNoShellSyntax(c, t.ToolVersion)
	})
	validatePlatformSupport(c, t.Platforms)
	c.child(field("acquisition"), func(c *collector) {
		if len(t.Acquisition) == 0 {
			c.fixf("declare at least one acquisition option", "is empty: nothing says how to obtain this tool")
		}
		for i, a := range t.Acquisition {
			c.child(index(i), a.validate)
		}
	})

	executables := executableSet(t.Executables)
	c.child(field("executables"), func(c *collector) {
		if len(t.Executables) == 0 {
			c.fixf("declare at least one executable", "is empty: a tool profile with no executable can run nothing")
		}
		validateNamed(c, t.Executables, func(e Executable) string { return e.Name }, func(c *collector, e Executable) { e.validate(c) })
	})
	if t.VersionProbe != nil {
		c.child(field("version_probe"), func(c *collector) { t.VersionProbe.validate(c, executables) })
	}
	capabilities := capabilitySet(t.Capabilities)
	c.child(field("capabilities"), func(c *collector) {
		validateNamed(c, t.Capabilities, func(cap Capability) string { return cap.ID }, func(c *collector, cap Capability) { cap.validate(c) })
	})
	c.child(field("actions"), func(c *collector) {
		if len(t.Actions) == 0 {
			c.fixf("declare at least one action", "is empty: a tool profile with no action offers nothing to run")
		}
		validateNamed(c, t.Actions, func(a Action) string { return a.ID }, func(c *collector, a Action) {
			a.validate(c, executables, capabilities, false)
			if a.SessionRole != "" {
				c.child(field("session_role"), func(c *collector) {
					c.fixf("remove it", "is set on a tool action; session roles describe game sessions and belong to engine profiles")
				})
			}
		})
	})

	// Every declared capability needs an action that implements it. A
	// capability nobody implements is a promise a pipeline will resolve against
	// and then fail on, at the point where the user has already waited.
	implemented := map[string]bool{}
	for _, a := range t.Actions {
		if a.Capability != "" {
			implemented[a.Capability] = true
		}
	}
	for i, cap := range t.Capabilities {
		if !implemented[cap.ID] {
			c.child(field("capabilities"), func(c *collector) {
				c.child(index(i), func(c *collector) {
					c.fixf("set `capability` on the action that implements it",
						"declares the capability %q, which no action in this profile implements", cap.ID)
				})
			})
		}
	}
	// Portability is part of validity, not a separate step before export. A
	// document with a machine path or a credential in it is invalid wherever it
	// is read, and catching it only at export would mean the author's own
	// Companion accepted it happily right up until they tried to share it.
	c.merge(CheckPortable(t))
	return c.problems.ErrorOrNil()
}

func validatePlatformSupport(c *collector, list []PlatformSupport) {
	c.child(field("platforms"), func(c *collector) {
		if len(list) == 0 {
			c.fixf("declare each platform and whether it is supported, unverified or unsupported",
				"is empty: a profile that claims nothing about platforms cannot be matched to a machine")
			return
		}
		seen := map[string]bool{}
		for i, ps := range list {
			c.child(index(i), func(c *collector) {
				ps.validate(c)
				k := ps.Platform.String()
				if seen[k] {
					c.addf("repeats the platform %s", k)
				}
				seen[k] = true
			})
		}
	})
}

// SupportFor reports what the profile claims about a platform. An undeclared
// platform is [Unsupported]: silence is not a promise.
func supportFor(list []PlatformSupport, p Platform) (Support, string) {
	for _, ps := range list {
		if ps.Platform == p {
			return ps.Status, ps.Note
		}
	}
	return Unsupported, "this profile makes no claim about " + p.String()
}

// SupportFor reports what this profile claims about a platform.
func (t *ToolProfile) SupportFor(p Platform) (Support, string) { return supportFor(t.Platforms, p) }

// Permissions implements [Profile]: the full set a user is asked to grant,
// ordered with the most consequential first.
func (t *ToolProfile) Permissions() []Permission {
	set := newPermissionSet()
	addExecutablePermission(set, t.Name, t.Executables)
	for _, a := range t.Acquisition {
		if id, risk, summary, ok := a.permission(); ok {
			set.add(id, risk, "%s", summary)
		}
	}
	for _, a := range t.Actions {
		addRootPermissions(set, a.Roots)
		addNetworkPermissions(set, a.Network)
		addEnvPermissions(set, a.Environment)
	}
	return set.list()
}

func addExecutablePermission(set *permissionSet, name string, executables []Executable) {
	names := make([]string, 0, len(executables))
	for _, e := range executables {
		names = append(names, e.Name)
	}
	set.add(PermRunExecutable, RiskHigh, "Run %s (%s) as a program on your computer.", name, strings.Join(names, ", "))
}
