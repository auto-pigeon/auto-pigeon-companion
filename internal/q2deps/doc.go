// Package q2deps reads a compiled Quake II map and says which files an engine
// will ask for when it loads it, and where each of them is on this machine.
//
// # Read from the BSP, not from the map source
//
// The same reason as `internal/q3deps`, and a Quake II-specific one besides: a
// face's contents and flags come from the `.wal` the COMPILER read, and the
// texture names an engine opens are the BSP's texinfo records — one per
// distinct (texture, flags, value, projection), including the ones only an
// animation chain reaches. The map source names textures the compiler may have
// dropped (a face of a clip brush is not drawn), and says nothing about the
// skins a model file names. So the runtime set is read from the file the
// engine reads.
//
// # What an engine looks for, measured in Yamagi Quake II 8.70's source
//
//   - every texinfo's `textures/<name>.wal` (`GetTexImage`; the renderer loads
//     every texinfo's image when the map loads, drawn or not);
//   - the worldspawn `sky` key's six faces, `env/<sky><rt|bk|lf|ft|up|dn>`,
//     as `.pcx` for a paletted renderer and `.tga` otherwise (`GetSkyImage`);
//   - each entity's `noise` key as `sound/<noise>`, with `.wav` added when the
//     key names none — what the game does before it asks for the sound;
//   - each entity's `model` key that is a file rather than an inline brush
//     model (`*N`), and the skins a `.md2` names in its own header.
//
// # What it does not read, and says so
//
// A model or sound the GAMECODE chooses by classname (`item_health`'s model,
// a monster's sounds) is not in the BSP; it is the game's own, and the base
// game supplies it. The music track a worldspawn's `sounds` number selects,
// and the next map a `target_changelevel` names, are not files of this map.
// Each of these is reported in [Report.Limits] rather than passed over.
package q2deps
