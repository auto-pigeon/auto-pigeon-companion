// Package aue finds the Auto-Pigeon Extractor this Companion ships with and
// drives it as a subprocess.
//
// # Shipped beside, never inside, never downloaded
//
// AUE is AGPL-3.0 and this repository is MIT. It is not embedded — the
// Companion once compiled it in with `//go:embed`, which put one program's
// bytes inside the other's artifact under the wrong licence, with nothing
// checking the staged binary. It is not downloaded either: the Companion once
// fetched it against a signed catalogue, and it downloads no program any more
// (operator, 2026-09-23).
//
// A release bundle carries it as a separate file beside the Companion —
// `auto-pigeon-extractor[.exe]` — listed with its SHA-256 in the bundle's
// `bundle-manifest.json`, under its own licence (build/bundle-sidecar.sh).
//
// # There are exactly two ways to an executable
//
//	bundled             the file beside this program; its digest is checked when
//	                    the bundle manifest lists it, and it must pass the
//	                    protocol handshake
//	developer override  AUCOM_AUE_BINARY, unverified, local, and labelled so
//
// There is no third, and no fallback between them. A bundled resolution that
// fails is an error the user reads; it never quietly becomes an override.
// [Provenance.Verified] travels with every runner, and every surface that shows
// an extractor shows it.
//
// # The protocol handshake happens before anything else
//
// The first thing a bundled runner does is ask `protocol --json` and compare
// the answer with [RequiredProtocol]: the majors must be equal and the minor at
// least the required one. A later major is a different contract, not a newer
// version of this one.
//
// # Why a subprocess and not a library
//
// AUE's packages all live under internal/, and Go's internal-package rule
// blocks a different module from importing them. AUE's CLI is its supported
// public surface, its subcommands print JSON on stdout, and the separation is
// what keeps this repository MIT while AUE is AGPL. AUE never appears in go.mod.
package aue
