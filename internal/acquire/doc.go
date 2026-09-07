// Package acquire gets a profile's executables onto this machine, by the four
// routes a profile may declare, and keeps the ones it downloaded in a cache it
// can prove the contents of.
//
// # The four routes, and what each one trusts
//
//   - `user_path` — the user names a file they already have. Nothing is
//     downloaded and nothing is checked against a catalogue, because there is
//     nothing to check against: the user is the authority, and pretending
//     otherwise would be theatre.
//   - `system_path` — found on PATH under a name the profile declares. Same
//     authority: whoever installed it is who the machine already trusts.
//   - `already_installed` — found at a declared place under a root the user has
//     already configured, usually shipped with a game.
//   - `managed_download` — fetched by the Companion. This is the only route
//     where this program is the one deciding what to put on somebody's disk,
//     and it is the only one with a verification chain: see internal/catalog.
//
// The first three are a resolution, not an acquisition. They are here because a
// caller asking "how do I get this profile's executables" wants one answer
// whichever route the user picked, and because the checks that *are* possible
// for them — the file exists, it is a regular file, it is executable, the path
// does not escape its root — are the same checks in all four cases.
//
// # Verified, then installed, then still checked
//
// A managed download goes: verify the catalogue chain, resolve the artifact,
// download to a temporary file under a size and time limit, check the exact
// size, check the digest, *then* open it. Extraction happens only after the
// bytes are known to be the right bytes, and it happens into a staging
// directory that is renamed into place as one operation, so a cache entry never
// exists in a half-written state.
//
// Verification does not stop there. A cache entry is checked again every time
// it is used — the executables are re-hashed against the install record — and
// the sticky revocation list is consulted on use as well as on install. An
// entry that was fine when it was installed and has been edited since is
// exactly the case that "verify at install" misses.
//
// # Offline
//
// Offline means: no network, and no fewer checks. What changes is only what is
// *available*. An installed entry carries the facts that were verified when it
// was installed — which catalogue serial, which signer, which digest — so using
// it needs no catalogue at all, and the digest and revocation checks run
// exactly as they do online. What offline cannot do is install something that
// is not already there, and it says so rather than reaching for the network.
//
// Expiry is the one rule that reads differently offline, and deliberately: a
// catalogue's expiry bounds how long *new* content may be accepted on its word.
// It is not a licence that runs out on a tool a user already has. Refusing to
// run an already-verified compiler because the machine has been off the network
// for a month would be a security theatre that costs a user their afternoon and
// buys nothing — revocation, which is the mechanism that actually withdraws
// something, is sticky and keeps working offline.
//
// # Licences
//
// The programs this package downloads are not this program. They are separate
// works, obtained from their own publishers, run as separate processes, and
// covered by their own licences — see [catalog.Aggregation], which is the
// sentence shown wherever one is described. Where a licence requires that a
// notice be shown, the notice is shown before the first download and the user's
// acknowledgement is recorded locally. That record is a note that they were
// shown it. It is not a licence grant, it does not come from Auto-Pigeon, and
// nothing here pretends that clicking changed anybody's obligations.
package acquire
