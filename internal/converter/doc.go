// Package converter finds the map converter this Companion ships with and drives it as a subprocess.
//
// # The converter is not the extractor
//
// `auto-pigeon-mapio` is a small program built in this repository from cmd/mapio (MIT) and the public
// go/mapio library of auto-pigeon-libraries (Apache-2.0). It converts map formats and does nothing
// else. A Companion used to run the separately licensed Auto-Pigeon Extractor for this; that path is
// gone (NEW_371), and nothing here falls back to one. An extractor at this path fails the handshake,
// because the handshake checks which program answered.
//
// # Shipped beside, never inside, never downloaded
//
// A release bundle carries it as its own file beside the Companion — `auto-pigeon-mapio[.exe]` —
// listed with its SHA-256 in the bundle's `bundle-manifest.json`, with its licences
// (build/bundle-sidecar.sh). It arrives only as part of a complete, verified Companion bundle.
//
// # There are exactly two ways to an executable
//
//	bundled             the file beside this program; its digest is checked when
//	                    the bundle manifest lists it, and it must pass the
//	                    protocol handshake
//	developer override  AUCOM_MAPIO_BINARY, unverified, local, and labelled so
//
// There is no third, and no fallback between them. A bundled resolution that
// fails is an error the user reads; it never quietly becomes an override.
// [Provenance.Verified] travels with every runner, and every surface that shows
// a converter shows it.
//
// # The protocol handshake happens before anything else
//
// The first thing a bundled runner does is ask `protocol --json`. The program must call itself
// auto-pigeon-mapio, its protocol must have the major of [RequiredProtocol] and at least its minor, and
// it must offer every direction a build needs. A later major is a different contract, not a newer
// version of this one.
//
// # Why a subprocess and not a library
//
// The library could be linked straight into the Companion, and one day may be. The process boundary is
// kept because it is already what makes a conversion cancellable, bounded in memory and output, and
// unable to take the Companion down with it, and because the Companion's own binary then stays free of
// any external module. Only cmd/mapio imports go/mapio.
package converter
