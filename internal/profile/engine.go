package profile

import (
	"fmt"
	"strings"
)

// EngineProfile describes a game engine the Companion can start.
//
// The five actions below are a closed vocabulary, and that is the point. An
// engine profile that could invent action names would be an engine profile
// whose "play" button might be a dedicated server, and the Companion would have
// no way to tell the user which. A closed set means the interface can say
// "Play", "Host", "Join" in words a person understands, and every engine that
// wants to be startable has to say which of those it is doing.

// The engine actions. Where an engine does not support one, it declares no
// action with that id; it never declares one that does something else.
const (
	// ActionPlayMap starts the engine on one map.
	ActionPlayMap = "play_map"
	// ActionPlayPackage starts the engine on a package — a mod directory, a
	// PAK, a PK3 — rather than on a single map.
	ActionPlayPackage = "play_package"
	// ActionJoinServer connects to somebody else's server.
	ActionJoinServer = "join_server"
	// ActionHostListen starts a game that other people can join, with a local
	// player in it.
	ActionHostListen = "host_listen"
	// ActionHostDedicated starts a server with no local player.
	ActionHostDedicated = "host_dedicated"
)

// EngineActions is the closed vocabulary, in the order a user meets them.
var EngineActions = []string{ActionPlayMap, ActionPlayPackage, ActionJoinServer, ActionHostListen, ActionHostDedicated}

// requiredSessionRole is what each engine action must declare itself to be.
// The mapping is fixed rather than authored: an engine that could label
// `host_dedicated` as a `client` would be a server presented as a single-player
// game.
var requiredSessionRole = map[string]SessionRole{
	ActionPlayMap:       SessionClient,
	ActionPlayPackage:   SessionClient,
	ActionJoinServer:    SessionClient,
	ActionHostListen:    SessionListen,
	ActionHostDedicated: SessionDedicated,
}

// EngineProfile is one engine, its content layouts and its actions.
type EngineProfile struct {
	Meta
	// GameProfile is required: an engine always belongs to a game family, and
	// that family is AUB's to define.
	GameProfile GameProfileRef `json:"game_profile" aucom:"required"`
	// Runtime is AUB's `engine_runtime` identifier for this engine —
	// `ironwail`, `quakespasm` — so a Game Profile that names a preferred
	// runtime and an engine profile that implements it can be matched without
	// a second naming scheme.
	Runtime string `json:"runtime" aucom:"required"`
	// EngineVersion is the upstream engine's version, not this document's.
	EngineVersion string `json:"engine_version" aucom:"required"`
	// LastQualified is the date the author last ran this profile against that
	// engine version, as `YYYY-MM-DD`. A curated profile that has not been
	// checked in two years is still useful; a curated profile that pretends it
	// was checked yesterday is not.
	LastQualified string              `json:"last_qualified,omitempty"`
	Platforms     []PlatformSupport   `json:"platforms" aucom:"required"`
	Acquisition   []AcquisitionOption `json:"acquisition" aucom:"required"`
	VersionProbe  *VersionProbe       `json:"version_probe,omitempty"`
	// ContentLayouts is how this engine expects content to be arranged.
	ContentLayouts []ContentLayout `json:"content_layouts,omitempty"`
	Executables    []Executable    `json:"executables" aucom:"required"`
	Actions        []Action        `json:"actions" aucom:"required"`
}

// Metadata implements [Profile].
func (e *EngineProfile) Metadata() Meta { return e.Meta }

// ActionList implements [Profile].
func (e *EngineProfile) ActionList() []Action { return e.Actions }

// ActionByID implements [Profile].
func (e *EngineProfile) ActionByID(id string) (Action, bool) { return findAction(e.Actions, id) }

// SupportFor reports what this profile claims about a platform.
func (e *EngineProfile) SupportFor(p Platform) (Support, string) { return supportFor(e.Platforms, p) }

// Validate implements [Profile].
func (e *EngineProfile) Validate() error {
	c := root("")
	e.Meta.validate(c, KindEngine)
	c.child(field("game_profile"), e.GameProfile.validate)
	c.child(field("runtime"), func(c *collector) {
		checkText(c, e.Runtime, 64, true)
		if e.Runtime != "" && e.Runtime != strings.ToLower(e.Runtime) {
			c.fixf("write it in lower case, as AUB does", "contains upper-case characters")
		}
	})
	c.child(field("engine_version"), func(c *collector) {
		checkText(c, e.EngineVersion, 64, true)
		checkNoShellSyntax(c, e.EngineVersion)
	})
	c.child(field("last_qualified"), func(c *collector) { checkDate(c, e.LastQualified) })
	validatePlatformSupport(c, e.Platforms)
	c.child(field("acquisition"), func(c *collector) {
		if len(e.Acquisition) == 0 {
			c.fixf("declare at least one acquisition option", "is empty: nothing says how to obtain this engine")
		}
		for i, a := range e.Acquisition {
			c.child(index(i), a.validate)
		}
	})

	executables := executableSet(e.Executables)
	c.child(field("executables"), func(c *collector) {
		if len(e.Executables) == 0 {
			c.fixf("declare at least one executable", "is empty: an engine profile with no executable can start nothing")
		}
		validateNamed(c, e.Executables, func(x Executable) string { return x.Name }, func(c *collector, x Executable) { x.validate(c) })
	})
	if e.VersionProbe != nil {
		c.child(field("version_probe"), func(c *collector) { e.VersionProbe.validate(c, executables) })
	}
	c.child(field("content_layouts"), func(c *collector) {
		validateNamed(c, e.ContentLayouts, func(l ContentLayout) string { return l.ID }, func(c *collector, l ContentLayout) { l.validate(c) })
	})

	c.child(field("actions"), func(c *collector) {
		if len(e.Actions) == 0 {
			c.fixf("declare at least one of: "+strings.Join(EngineActions, ", "),
				"is empty: an engine profile with no action cannot be started")
		}
		validateNamed(c, e.Actions, func(a Action) string { return a.ID }, func(c *collector, a Action) {
			a.validate(c, executables, nil, true)
			want, known := requiredSessionRole[a.ID]
			if !known {
				c.child(field("id"), func(c *collector) {
					c.fixf("engine actions are: "+strings.Join(EngineActions, ", ")+"; an engine that does not support one simply omits it",
						"is %q, which is not an engine action", a.ID)
				})
				return
			}
			if a.SessionRole != want {
				c.child(field("session_role"), func(c *collector) {
					c.fixf(fmt.Sprintf("set it to %q", want),
						"is %q, but %q always starts a %s", a.SessionRole, a.ID, want)
				})
			}
			if a.Capability != "" {
				c.child(field("capability"), func(c *collector) {
					c.fixf("remove it", "is set on an engine action; capabilities describe tools, and an engine action is named by its own closed vocabulary")
				})
			}
		})
	})
	// Portability is part of validity, not a separate step before export. A
	// document with a machine path or a credential in it is invalid wherever it
	// is read, and catching it only at export would mean the author's own
	// Companion accepted it happily right up until they tried to share it.
	c.merge(CheckPortable(e))
	return c.problems.ErrorOrNil()
}

// checkDate validates a `YYYY-MM-DD` date without pulling in time parsing for
// something that is only ever displayed.
func checkDate(c *collector, value string) {
	if value == "" {
		return
	}
	if len(value) != 10 || value[4] != '-' || value[7] != '-' {
		c.fixf("write it as YYYY-MM-DD", "is %q", value)
		return
	}
	for i, ch := range value {
		if i == 4 || i == 7 {
			continue
		}
		if ch < '0' || ch > '9' {
			c.fixf("write it as YYYY-MM-DD", "is %q", value)
			return
		}
	}
}

// Permissions implements [Profile].
//
// An engine gets one permission a tool does not: hosting. Starting a listen or
// dedicated server puts a socket on the network with the user's machine behind
// it, and a user who pressed something labelled "play" is entitled to be asked
// about that separately from being asked whether the engine may run at all.
func (e *EngineProfile) Permissions() []Permission {
	set := newPermissionSet()
	addExecutablePermission(set, e.Name, e.Executables)
	for _, a := range e.Acquisition {
		if id, risk, summary, ok := a.permission(); ok {
			set.add(id, risk, "%s", summary)
		}
	}
	for _, a := range e.Actions {
		addRootPermissions(set, a.Roots)
		addNetworkPermissions(set, a.Network)
		addEnvPermissions(set, a.Environment)
		switch a.SessionRole {
		case SessionListen:
			set.add("host_listen", RiskHigh, "Host a game other people can connect to, from this computer.")
		case SessionDedicated:
			set.add("host_dedicated", RiskHigh, "Run a dedicated game server on this computer that other people can connect to.")
		}
	}
	return set.list()
}
