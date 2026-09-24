package profile

import (
	"fmt"
	"net"
	"strings"
)

// Portability, enforced on the document's own text.
//
// The type system already keeps a [github.com/auto-pigeon/auto-pigeon-companion/internal/binding.LocalBinding]
// out of a profile: that type lives in a package which imports this one, so no
// profile type can contain it and a test asserts the import direction. That
// handles the structural mistake.
//
// It does not handle the content mistake, which is the likelier one. Nothing in
// the type system stops somebody pasting `/home/andrea/quake` into a `purpose`
// string, or an AUB token into a description, and then publishing it. So every
// string in a portable document is scanned for the four things that must never
// leave a machine: an absolute path, a home directory, a network location, and
// anything shaped like a credential.
//
// The scan runs over the *canonical* form rather than over the Go value, which
// means it cannot be bypassed by a member added later and forgotten here.

// CheckPortable reports every place a document says something that is true only
// on the machine it was written on, or that should never have been written down
// at all.
//
// It is run by [Canonical] and by every Validate, so a document cannot be
// digested, published or imported without passing it.
func CheckPortable(doc any) error {
	// Accepts either a typed profile or an already-decoded tree, and walks the
	// tree in both cases. Scanning the tree rather than the Go value is what
	// makes the check total: a member added to a struct next year is scanned
	// without anybody remembering to add it here.
	tree, err := toTree(doc)
	if err != nil {
		return err
	}
	c := root("")
	scanPortable(c, tree)
	return c.problems.ErrorOrNil()
}

func scanPortable(c *collector, value any) {
	switch v := value.(type) {
	case map[string]any:
		for _, k := range sortedKeys(v) {
			c.child(field(k), func(c *collector) { scanPortable(c, v[k]) })
		}
	case []any:
		for i, item := range v {
			c.child(index(i), func(c *collector) { scanPortable(c, item) })
		}
	case string:
		checkPortableString(c, v)
	}
}

// checkPortableString is the whole rule, applied to one string.
func checkPortableString(c *collector, s string) {
	if s == "" {
		return
	}
	if fault := absolutePathFault(s); fault != "" {
		c.fixf("name a root role — "+strings.Join(rootRoles, ", ")+" — and let the local binding say where it is on this machine",
			"contains %s, which is a path on one machine and wrong on every other", fault)
		return
	}
	if strings.HasPrefix(s, "~/") || strings.HasPrefix(s, "~\\") {
		c.fixf("name a root role instead", "starts with `~`, a home directory that differs per user")
		return
	}
	if fault := networkLocationFault(s); fault != "" {
		c.fixf("a profile names hosts by DNS name; the machine it runs on is not the profile's business",
			"contains %s", fault)
		return
	}
	if fault := secretFault(s); fault != "" {
		c.fixf("remove it, and treat it as disclosed — anything written into a document that gets shared is disclosed",
			"contains %s", fault)
	}
}

// absolutePathFault recognises the three absolute-path spellings that matter:
// POSIX, Windows drive-letter, and a UNC share.
func absolutePathFault(s string) string {
	for _, token := range splitOnSpace(s) {
		switch {
		case strings.HasPrefix(token, "//") || strings.HasPrefix(token, `\\`):
			// A UNC path, or a protocol-relative URL. Both are locations.
			return fmt.Sprintf("the network path %q", token)
		case strings.HasPrefix(token, "/"):
			// Not every leading slash is a path: a licence identifier is not,
			// and neither is a lone separator. Two segments is the point where
			// it is a filesystem location and not a punctuation mark.
			if strings.Count(strings.TrimSuffix(token, "/"), "/") >= 2 {
				return fmt.Sprintf("the absolute path %q", token)
			}
		case len(token) >= 3 && isDriveLetter(token[0]) && token[1] == ':' && (token[2] == '\\' || token[2] == '/'):
			return fmt.Sprintf("the absolute path %q", token)
		}
	}
	return ""
}

func isDriveLetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// networkLocationFault recognises an address rather than a name: an IP literal,
// a loopback name, or a `host:port`.
//
// The `.env` rule this repository already runs on says a component may never
// compile in where another component lives, for a reason that was paid for: a
// hardcoded `127.0.0.1` turned a misconfiguration into a plausible wrong answer
// on a LAN, where loopback means *the reader's own machine*. A shared profile
// document is the same mistake with a wider blast radius, so the same rule
// applies to it.
func networkLocationFault(s string) string {
	for _, token := range splitOnSpace(s) {
		bare := strings.Trim(token, `"'(),;`)
		// `addressed` records that this token is being *used* as a location:
		// it carries a scheme, a port or a path. A bare dotted quad might be
		// something else entirely — `engine_version: "1.2.3.4"` is a version,
		// and net.ParseIP is happy to call it an address — so a bare literal is
		// only refused when it is one of the addresses that could not be
		// anything else.
		addressed := false
		for _, scheme := range []string{"https://", "http://"} {
			if strings.HasPrefix(bare, scheme) {
				bare, addressed = strings.TrimPrefix(bare, scheme), true
			}
		}
		if i := strings.IndexByte(bare, '/'); i >= 0 {
			bare, addressed = bare[:i], true
		}
		host := bare
		if h, port, err := net.SplitHostPort(bare); err == nil && port != "" {
			host, addressed = h, true
		}
		host = strings.Trim(host, "[]")
		if host == "" {
			continue
		}
		lower := strings.ToLower(host)
		if lower == "localhost" || strings.HasSuffix(lower, ".localhost") || lower == "localhost.localdomain" {
			return fmt.Sprintf("the loopback name %q", host)
		}
		ip := net.ParseIP(host)
		if ip == nil {
			continue
		}
		switch {
		case ip.IsLoopback():
			return fmt.Sprintf("the loopback address %q", host)
		case ip.IsPrivate(), ip.IsLinkLocalUnicast(), ip.IsUnspecified():
			return fmt.Sprintf("the private address %q", host)
		case addressed:
			return fmt.Sprintf("the IP address %q", host)
		}
	}
	return ""
}

// secretFault recognises the credential shapes that actually turn up in
// configuration a human pasted: a bearer token, a PEM private key, an AUB or
// PocketBase session token, a URL with credentials in it.
//
// Like [looksSecret] this is a blunt instrument and is not the boundary. It is
// here so that the common accident is caught at the point it happens rather
// than at the point somebody else reads the file.
func secretFault(s string) string {
	lower := strings.ToLower(s)
	switch {
	case strings.Contains(s, "-----BEGIN") && strings.Contains(s, "PRIVATE KEY"):
		return "a PEM private key"
	case strings.Contains(lower, "authorization:"), strings.Contains(lower, "bearer "):
		return "an authorization header"
	}
	for _, token := range splitOnSpace(s) {
		if at := strings.IndexByte(token, '@'); at > 0 && strings.Contains(token[:at], ":") &&
			(strings.HasPrefix(token, "https://") || strings.HasPrefix(token, "http://")) {
			return "a URL with credentials in it"
		}
		if looksLikeJWT(token) {
			return "a JSON Web Token"
		}
	}
	return ""
}

// looksLikeJWT recognises the three-segment base64url shape. AUB issues these,
// and a session token pasted into a description is the exact accident this
// catches.
func looksLikeJWT(token string) bool {
	parts := strings.Split(strings.Trim(token, `"',;`), ".")
	if len(parts) != 3 {
		return false
	}
	if !strings.HasPrefix(parts[0], "eyJ") {
		return false
	}
	for _, p := range parts {
		if len(p) < 8 {
			return false
		}
		for i := 0; i < len(p); i++ {
			ch := p[i]
			if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '=') {
				return false
			}
		}
	}
	return true
}

func splitOnSpace(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
}

// checkPublicHost validates a DNS name a profile says it contacts.
func checkPublicHost(c *collector, host string) {
	if host == "" {
		c.addf("is an empty host name")
		return
	}
	if len(host) > 253 {
		c.addf("is %d bytes long, over the 253-byte DNS limit", len(host))
		return
	}
	if strings.ContainsAny(host, " \t/:@") {
		c.fixf("write a bare DNS name, with no scheme, port, path or credentials", "is not a bare DNS name")
		return
	}
	if fault := networkLocationFault(host); fault != "" {
		c.fixf("name the host by DNS name; an address is a location, and a private or loopback one is the reader's own machine",
			"is %s", fault)
		return
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if label == "" {
			c.addf("has an empty label in %q", host)
			return
		}
		for i := 0; i < len(label); i++ {
			ch := label[i]
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				c.fixf("use letters, digits and hyphens", "has an invalid character %q in %q", string(label[i]), host)
				return
			}
		}
	}
	if !strings.Contains(host, ".") {
		c.fixf("use a fully qualified name", "is %q, which is not a fully qualified DNS name", host)
	}
}
