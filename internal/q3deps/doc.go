// Package q3deps reads a Quake III map and says what it depends on, so that a
// PK3 which does not carry one of those things is refused rather than written.
//
// # Why this exists and what it is not
//
// Q3Map2 will compile a map whose textures are missing. Measured on 2.5.17n: a
// face naming a shader with no image and no script produces
// `WARNING: Couldn't find image for shader …`, the compiler exits 0 and the BSP
// is written. With no game data bound at all the compile still succeeds. So the
// compiler cannot be the thing that tells a user their package is incomplete —
// it is not trying to, and a warning in a log nobody reads is not a review.
//
// This package is that review. It is deliberately NOT a filter: nothing here
// removes, substitutes or repairs anything. It reads the map, resolves each
// reference against the archive that is about to be written, the user's own
// content and the base game, and reports what it could not account for.
// `AUP/AUCOM 216` requires that an unknown dependency "generate an explicit
// review, not a silently incomplete PK3", and a review a program resolves on
// the user's behalf is not one.
//
// # What it reads, exactly, and what it therefore cannot see
//
// The scope is what could be established by running Q3Map2 against real maps
// and comparing what it looked for with what this parser found. That comparison
// is the test `TestTheScanAgreesWithTheCompilerAboutWhatIsMissing`, and it is
// the reason this package claims what it claims and nothing more.
//
// Read:
//
//   - every brush face's shader name, in the legacy format and in `brushDef3`;
//   - every `patchDef2` and `patchDef3` shader name;
//   - the entity keys that name a file: `model`, `noise` and `music`;
//   - the user's own `scripts/*.shader`, for the images each shader stage names
//     — `map`, `clampmap`, `animMap`, `videoMap`, `skyParms`, `lightimage` and
//     the editor image.
//
// Not read, and each of these produces a review rather than silence:
//
//   - what a `.md3` or `.ase` model references internally. A model is a binary
//     file naming its own shaders, and a scan that skipped it and said nothing
//     would be the silent incompleteness this package exists to prevent.
//   - what a base-game shader pulls in. The base game's scripts ARE read, and
//     read out of `pak0.pk3` rather than only loose, because a scan that
//     reported every `common/*` shader as missing would be a review a user
//     learns to click past. But a shader defined there stops there: what it
//     references is inside somebody else's PK3, and the answer a person needs
//     is that it is not theirs to ship.
//   - anything a mod's gamecode loads by name at runtime.
//
// # The prefix rule, which is measured and is a real authoring mistake
//
// A face or patch names a shader WITHOUT the `textures/` prefix — `aucom/wall`,
// not `textures/aucom/wall` — and Q3Map2 prepends it. Measured both ways: a map
// written with the prefix produces
// `WARNING: Couldn't find image for shader textures/textures/aucom/wall`, which
// reads like a missing file rather than like a doubled prefix. So this package
// normalizes the way the compiler does and names the doubling when it sees it.
package q3deps
