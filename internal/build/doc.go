// Package build runs a pipeline: several supervised jobs, in order, with the
// files wired between them and a manifest of what happened.
//
// # It runs nothing itself
//
// Every process a build starts is a job, submitted to the same [job.Service]
// that `companion job run` uses, resolved by the same [profile.Resolve], staged
// into the same kind of workspace and recorded in the same store. There is no
// pipeline executor. What this package adds is *ordering and wiring*: which
// step runs next, which file from the last step becomes which input of the next
// one, and what to write down about it.
//
// That matters because a pipeline is where a shortcut would be tempting. Three
// stages that share a directory and skip the staging, the containment checks
// and the per-stage record would be faster to write and would quietly undo
// every guarantee ADR-0003 makes. So the stages do not share a directory: each
// one gets its own job, and the files move between them through the build
// directory, which is the `build_root` role.
//
// # The build directory
//
//	<builds>/<build id>/
//	  manifest.json          what happened, and what it was computed from
//	  input/<name>/…         the user's files, copied once
//	  stage/<step>/<out>/…   what each step produced, for the next one to read
//	  output/<name>/…        what the pipeline declared it publishes
//
// Inputs are copied in rather than referenced, for the reason [job] copies
// them: `vis` and `light` rewrite the file they are handed, and a build that
// passed the user's own BSP through would be a build that modified it.
//
// # What the manifest is for
//
// A manifest answers "what produced this BSP" without needing the machine that
// produced it. It records the pipeline document's digest, each step's exact
// argv, the digest of every executable that ran, the digest of every input and
// every output, and every diagnostic the tools emitted — and then a
// [Manifest.ReproducibleKey] over the subset of that which determines the
// result. Two builds of the same inputs with the same tools have the same key
// on any machine; the paths, times and job ids that differ between them are
// deliberately not in it.
//
// # Diagnostics classify, they never suppress
//
// A step's raw log is the job's, kept whole. What the manifest adds is the
// classification the profile's rules produced, so that "the map leaks" is a
// structured fact and not a line somebody has to notice. `--strict` turns an
// error-severity diagnostic into a failed build, which is what a gate wants;
// the default leaves the tool's own verdict alone, which is what a person at a
// keyboard wants.
package build
