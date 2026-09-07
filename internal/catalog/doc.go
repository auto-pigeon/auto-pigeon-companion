// Package catalog is the signed acquisition catalogue: the only thing in this
// program that may say where a downloadable executable lives, how big it is and
// what its digest must be.
//
// # Why a profile does not carry a URL
//
// A profile declares *how* a tool can be obtained — from PATH, from a directory
// the user already has, by a managed download — and never *where from*. If the
// location were in the document, then rotating a mirror or withdrawing a
// compromised build would mean republishing every profile that pointed at it,
// and a profile a user had already reviewed and granted would keep pointing at
// the old bytes. Withdrawal has to be faster than republication, so the two
// documents are separate and versioned independently.
//
// # The three documents
//
//   - **Trust anchors.** A small set of Ed25519 public keys this build accepts
//     as the root of the catalogue. They are the only thing not itself signed,
//     which is why they are configuration rather than a download: see
//     [LoadAnchors].
//   - **The keyring**, signed by the anchors. It says which keys may sign a
//     catalogue, for how long, and which keys are revoked.
//   - **The catalogue**, signed by keys the keyring names. It maps a package
//     and version and platform to an immutable URL, an exact size, a SHA-256
//     digest, a signer, the upstream source, the licence and the
//     corresponding-source offer.
//
// Two levels rather than one because the two have different lifetimes. A
// catalogue changes whenever a tool is published; a keyring changes when a key
// does. Signing every catalogue with the anchor would mean the anchor's private
// key is online, and an anchor whose private key is online is an anchor that
// cannot be the thing you fall back to.
//
// # What verification actually checks
//
// In order, and all of them, every time:
//
//  1. The envelope's payload is *exactly* the canonical encoding of what it
//     decodes to. This is what makes a signature mean something: a document two
//     JSON parsers read differently — a duplicated member, say — is a document
//     whose signature covers one reading and whose behaviour is the other.
//  2. At least one signature is by a key that is permitted to sign this kind of
//     document, is inside its validity window, and is not revoked.
//  3. The document has not expired.
//  4. The document's serial is not lower than the highest this machine has
//     already accepted. A signed old catalogue is a valid signature over the
//     wrong answer, and replaying one is how a withdrawn build comes back.
//  5. Nothing it names is revoked, including by a revocation this machine saw
//     once and has remembered ever since.
//
// A failure at any step is a refusal. There is no path in this package that
// returns a usable catalogue, key or artifact without all five having passed,
// and no caller can ask it to skip one.
//
// # Emergency revocation, and why it is sticky
//
// Revocation is published in the two signed documents: a key is revoked in the
// keyring, an artifact is revoked by digest in the catalogue. Both are recorded
// in local state the moment they are seen and are never forgotten — a later
// document cannot un-revoke either. That asymmetry is the mechanism. Revocation
// that a newer signed document could reverse would be undone by exactly the
// party you are revoking against, and revocation that lived only in a fetched
// document would be undone by unplugging the network.
//
// It is also what makes revocation work offline: the sticky list is consulted
// when an already-installed tool is used, not only when one is installed.
//
// # Keys, and what is not in this repository
//
// No private key is in this repository and none is compiled into this build.
// The anchors are loaded from a file an operator installs; a build with none
// refuses every managed download rather than falling back to an unverified one.
// The keys under `testdata/` are generated fixtures, are labelled as such, and
// sign nothing outside the tests. `README.md` documents the operational signing
// procedure and `companion catalog` performs it.
package catalog
