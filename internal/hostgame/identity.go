package hostgame

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// IdentityPrefix marks a value this program minted, so a stray one in a log is
// recognisable for what it is.
const IdentityPrefix = "aucom-"

// ProcessIdentity is the evidence a restart presents when it reclaims a lease.
//
// # What it is made of, and why each part is in it
//
// The INSTALLATION's own directory, the map being served, and the address it is
// served on. Those three are exactly what is stable across the event this exists
// for — a Companion that was restarted and is hosting the same game again — and
// exactly what distinguishes it from a second server the same person is entitled
// to run beside it.
//
// # Why it is not a process id
//
// A PID is different after precisely the event reclaim exists for, so it could
// never reclaim anything; and it is readable by anything else on the machine,
// so it would be a discriminator anybody local could guess. AUB scopes this value
// to the account regardless — a value a client chooses is never an authority
// there — so what is wanted here is stability, not secrecy.
//
// # Why it is hashed
//
// The inputs include an absolute path on this machine. It is sent to a server
// and stored beside a listing, and a local filesystem path is exactly the sort of
// thing `AGENTS.md` refuses to put in one. The digest is stable, opaque and
// carries none of it.
func ProcessIdentity(installRoot, mapID, endpointHost string, endpointPort int) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"auto-pigeon-companion/hosted-game/1",
		strings.TrimRight(installRoot, "/\\"),
		mapID,
		strings.ToLower(strings.TrimSpace(endpointHost)),
		strconv.Itoa(endpointPort),
	}, "\x00")))

	// Sixteen bytes. The value is a discriminator within one account's leases, not
	// a secret and not a key, and a 32-byte hex string in a listing row would be
	// noise for no benefit.
	return IdentityPrefix + hex.EncodeToString(sum[:16])
}

// SplitEndpoint parses a `host:port` a user typed into the two fields a
// registration carries.
//
// It refuses rather than guessing a port. A default port here would be this
// program deciding which port somebody's server is on, and being wrong about it
// produces a listing that sends strangers to a closed socket — which looks
// exactly like a firewall problem and is not one.
func SplitEndpoint(value string) (string, int, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return "", 0, fmt.Errorf("hostgame: %q is not an address and a port, like 203.0.113.4:26000", value)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("hostgame: %q is not a port", portText)
	}

	return strings.Trim(host, "[]"), port, nil
}
