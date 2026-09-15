package profile

import (
	"fmt"
	"sort"
	"strings"
)

// What a profile is allowed to reach, declared rather than discovered.
//
// The unit a user grants is a [Permission] — a short, stable id with a sentence
// attached. Grants are recorded against permission ids and a document digest,
// so "I let this compiler write into my project" survives a profile update
// while "…and now it also wants the network" does not.

// Access is read, write, or both, for one declared root.
type Access string

const (
	AccessRead      Access = "read"
	AccessReadWrite Access = "read_write"
)

var accessModes = []string{string(AccessRead), string(AccessReadWrite)}

// RootRef is a filesystem root an action needs, named by role.
//
// Roles, not paths. A profile that could name `/home/someone/maps` would be
// carrying a machine identifier, would be wrong on every other machine, and
// would have moved the decision about what a program may touch from the person
// who owns the files to the person who wrote the document.
type RootRef struct {
	Role   string `json:"role" aucom:"required"`
	Access Access `json:"access" aucom:"required"`
	// Purpose is shown in the permission summary. It is required for write
	// access: "this tool writes into your project" is not a sentence a user can
	// act on without knowing what it writes.
	Purpose string `json:"purpose,omitempty"`
	// Optional says the action runs whether or not this root is set on the
	// machine. An argument that uses it is then written with `when.root`, so it
	// is passed only when there is a folder to pass (NEW_244D: EricW's
	// `-wadpath`, which finds `gfx/metal.wad` when a texture folder is set and
	// is simply left out when none is). A required root stays the default.
	Optional bool `json:"optional,omitempty"`
}

func (r RootRef) validate(c *collector) {
	c.child(field("role"), func(c *collector) {
		if !contains(rootRoles, r.Role) {
			c.fixf("use one of: "+strings.Join(rootRoles, ", "), "is %q, which is not a known root role", r.Role)
		}
	})
	c.child(field("access"), func(c *collector) {
		if !contains(accessModes, string(r.Access)) {
			c.fixf("use one of: "+strings.Join(accessModes, ", "), "is %q", r.Access)
		}
	})
	c.child(field("purpose"), func(c *collector) {
		checkText(c, r.Purpose, maxSummaryLength, false)
		if r.Access == AccessReadWrite && strings.TrimSpace(r.Purpose) == "" {
			c.fixf("say what is written there", "is empty, and write access must say what it is for")
		}
	})
	c.child(field("optional"), func(c *collector) {
		if r.Optional && r.Role == RootWorkspace {
			c.fixf("drop `optional`", "is set on the workspace, which every job has")
		}
	})
}

// NetworkNeed is whether an action reaches the network, and where.
//
// Hosts are DNS names. An IP literal is refused, and so is anything that
// resolves conceptually to "this machine": a profile that could name
// `127.0.0.1` or a private range would be asking a user to approve a
// connection to their own services, described as if it were the tool's own
// upstream. The address a user types into `join_server` is a runtime value and
// is not this member.
type NetworkNeed struct {
	Required bool `json:"required"`
	// Purpose is required when Required is set.
	Purpose string `json:"purpose,omitempty"`
	// Hosts is the set of DNS names the action contacts. An empty list with
	// Required set means "the tool contacts hosts we cannot enumerate", which
	// is a much larger permission and is summarised as one.
	Hosts []string `json:"hosts,omitempty"`
}

func (n NetworkNeed) validate(c *collector) {
	c.child(field("purpose"), func(c *collector) {
		checkText(c, n.Purpose, maxSummaryLength, false)
		if n.Required && strings.TrimSpace(n.Purpose) == "" {
			c.fixf("say what it connects for", "is empty, and an action that needs the network must say why")
		}
	})
	c.child(field("hosts"), func(c *collector) {
		if len(n.Hosts) > 0 && !n.Required {
			c.fixf("set `required` to true, or remove the hosts", "lists hosts, but `required` is false")
		}
		if len(n.Hosts) > maxListLength {
			c.addf("has %d entries, over the %d limit", len(n.Hosts), maxListLength)
			return
		}
		for i, host := range n.Hosts {
			c.child(index(i), func(c *collector) { checkPublicHost(c, host) })
		}
	})
}

// EnvironmentPolicy is what an action's process inherits.
//
// The default is nothing. A build tool that inherits the user's whole
// environment inherits their tokens, their proxy credentials and their
// `LD_PRELOAD`, and there is no way to review that because its contents are not
// in the document. So the document lists names it wants passed through, and the
// executor passes those and nothing else.
type EnvironmentPolicy struct {
	// Inherit lists environment variable *names* to pass through from the
	// Companion's own environment. Names only: a profile never sees a value.
	Inherit []string `json:"inherit,omitempty"`
	// Set gives literal or templated values for variables the tool needs.
	Set map[string]string `json:"set,omitempty"`
}

// envNameDenied is the set of inheritable names that are refused outright,
// because passing them through changes what executable runs or what is loaded
// into it. Allowing a profile to ask for `LD_PRELOAD` would make "run this
// program with these arguments" mean something else entirely.
var envNameDenied = []string{
	"LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT", "DYLD_INSERT_LIBRARIES",
	"DYLD_LIBRARY_PATH", "DYLD_FRAMEWORK_PATH", "PATH", "IFS", "BASH_ENV",
	"ENV", "SHELL", "PYTHONPATH", "PERL5LIB", "NODE_OPTIONS",
}

func (e EnvironmentPolicy) validate(c *collector, s scope) {
	c.child(field("inherit"), func(c *collector) {
		if len(e.Inherit) > maxListLength {
			c.addf("has %d entries, over the %d limit", len(e.Inherit), maxListLength)
			return
		}
		for i, name := range e.Inherit {
			c.child(index(i), func(c *collector) { checkEnvName(c, name, true) })
		}
	})
	c.child(field("set"), func(c *collector) {
		if len(e.Set) > maxListLength {
			c.addf("has %d entries, over the %d limit", len(e.Set), maxListLength)
			return
		}
		for _, name := range sortedKeys(e.Set) {
			c.child(key(name), func(c *collector) {
				checkEnvName(c, name, false)
				value := e.Set[name]
				checkText(c, value, maxArgLength, false)
				checkNoShellSyntax(c, value)
				t, err := parseTemplate(value)
				if err != nil {
					c.fixf("write `{namespace.name}` placeholders", "%v", err)
					return
				}
				s.check(c, t)
			})
		}
	})
}

func checkEnvName(c *collector, name string, inheriting bool) {
	if name == "" {
		c.addf("is an empty environment variable name")
		return
	}
	if len(name) > 64 {
		c.addf("is %d bytes long, over the 64-byte limit", len(name))
		return
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_') {
			c.fixf("use letters, digits and underscores", "contains %q", string(name[i]))
			return
		}
	}
	upper := strings.ToUpper(name)
	if contains(envNameDenied, upper) {
		c.fixf("this variable changes what executable runs or what is loaded into it, so it is never passed through",
			"names %s, which may not be inherited or set", upper)
		return
	}
	if inheriting && looksSecret(upper) {
		c.fixf("a profile may not ask for a credential from the Companion's environment",
			"names %s, which reads like a credential", upper)
	}
}

// looksSecret is a deliberately blunt name filter. It cannot be complete, and
// it is not the security boundary — the boundary is that nothing is inherited
// unless it is listed. It catches the case where a profile asks for something
// that is obviously a credential, so the refusal names the problem rather than
// leaving it to a user to notice `AWS_SECRET_ACCESS_KEY` in a list of eleven.
func looksSecret(upper string) bool {
	for _, marker := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "APIKEY", "API_KEY", "CREDENTIAL", "SESSION", "COOKIE", "PRIVATE_KEY", "AUTH"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

// Permission is one thing a user grants, as an id and a sentence.
type Permission struct {
	// ID is stable and comparable. Grants are recorded by id, so it must not
	// change when the summary wording does.
	ID string `json:"id"`
	// Summary is one sentence in the second person, describing the effect and
	// not the mechanism.
	Summary string `json:"summary"`
	// Risk orders the review: high first.
	Risk Risk `json:"risk"`
}

// Risk is how much a permission gives away.
type Risk string

const (
	RiskHigh   Risk = "high"
	RiskMedium Risk = "medium"
	RiskLow    Risk = "low"
)

func riskOrder(r Risk) int {
	switch r {
	case RiskHigh:
		return 0
	case RiskMedium:
		return 1
	}
	return 2
}

// Permission ids. Stable strings, because a recorded grant outlives a release.
const (
	PermRunExecutable = "run_executable"
	PermNetwork       = "network"
	PermNetworkAny    = "network_unenumerated"
	PermDownload      = "acquire_download"
)

// PermRead and PermWrite build the per-root permission ids.
func PermRead(role string) string  { return "read:" + role }
func PermWrite(role string) string { return "write:" + role }

// PermEnv builds the per-variable environment permission id.
func PermEnv(name string) string { return "env:" + strings.ToUpper(name) }

// permissionSet accumulates permissions without duplicates and sorts them for
// display. Two actions that both write the project root are one permission, and
// a review that listed it twice would be teaching the user to skim.
type permissionSet struct {
	byID map[string]Permission
}

func newPermissionSet() *permissionSet { return &permissionSet{byID: map[string]Permission{}} }

func (s *permissionSet) add(id string, risk Risk, format string, args ...any) {
	if _, seen := s.byID[id]; seen {
		return
	}
	s.byID[id] = Permission{ID: id, Summary: fmt.Sprintf(format, args...), Risk: risk}
}

func (s *permissionSet) list() []Permission {
	out := make([]Permission, 0, len(s.byID))
	for _, p := range s.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := riskOrder(out[i].Risk), riskOrder(out[j].Risk); a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// rootPhrase turns a root role into something a user recognises. The permission
// summary is read by somebody deciding whether to trust a stranger's document,
// and `content_root` is not a phrase they have ever seen.
func rootPhrase(role string) string {
	switch role {
	case RootWorkspace:
		return "a scratch folder created for this job"
	case RootProject:
		return "your map project folder"
	case RootGame:
		return "your installed game folder"
	case RootContent:
		return "the folder built content is published into"
	case RootToolInstall:
		return "this tool's own installation folder"
	case RootToolCache:
		return "the Companion's download cache"
	case RootBuild:
		return "the folder this build's stages hand files through"
	}
	return role
}

// rootRisk: writing outside the job's own scratch space is where a mistake
// costs somebody their files, so it is the line the risk levels are drawn at.
func rootRisk(role string, access Access) Risk {
	if access == AccessRead {
		if role == RootWorkspace || role == RootToolInstall || role == RootToolCache || role == RootBuild {
			return RiskLow
		}
		return RiskMedium
	}
	if role == RootWorkspace || role == RootBuild {
		return RiskLow
	}
	return RiskHigh
}

func addRootPermissions(set *permissionSet, roots []RootRef) {
	for _, r := range roots {
		if r.Access == AccessReadWrite {
			set.add(PermWrite(r.Role), rootRisk(r.Role, r.Access), "Create and change files in %s.", rootPhrase(r.Role))
		}
		set.add(PermRead(r.Role), rootRisk(r.Role, AccessRead), "Read files in %s.", rootPhrase(r.Role))
	}
}

func addNetworkPermissions(set *permissionSet, n *NetworkNeed) {
	if n == nil || !n.Required {
		return
	}
	if len(n.Hosts) == 0 {
		set.add(PermNetworkAny, RiskHigh, "Connect to hosts on the internet that this profile does not list.")
		return
	}
	set.add(PermNetwork, RiskMedium, "Connect to %s.", strings.Join(n.Hosts, ", "))
}

func addEnvPermissions(set *permissionSet, e *EnvironmentPolicy) {
	if e == nil {
		return
	}
	for _, name := range e.Inherit {
		set.add(PermEnv(name), RiskMedium, "Pass your %s environment variable to the program.", strings.ToUpper(name))
	}
}
