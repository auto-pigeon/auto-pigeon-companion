# EricW qbsp Quake II leak-test vectors (`Q2_010`)

The Auto-Pigeon build of ericw-tools, release 1.4518 (`2.0.0-alpha11-32-g0c787d7a+auto-pigeon.1.4518`),
run as `qbsp -nocolor -q2bsp -basedir <baseq2> -gamedir <content> -leaktest <map> level.bsp` on small
authored control maps: what it printed on its standard output (`logs/`, plain, `.quiet` and `.verbose`)
and the point files it wrote (`pts/`). Only directory names were changed (`/q2010/…`). The same files
are the editor's vectors too (`auto-pigeon/frontend/src/test/fixtures/leak-ericw-q2`); the maps, the
WAL textures and their generator (`controls/gen_controls.py`) are here, so both readings are held to one set of
measurements. qbsp's own `level.log` FILE is not used: it leaves out every `---- Stage ----` heading.

| control | what it is | exit | left on disk |
| --- | --- | --- | --- |
| `a_sealed` | six structural brushes, a light and an `info_player_start` inside | 0 | BSP (IBSP 38), PRT, texinfo |
| `b_gap` | the east wall with an 8-unit slot through it | 1 | `.pts` (10 points, first = the light), `.leak.prt` |
| `c_outside_light` | sealed, with a second light outside the shell | 1 | `.pts` (6 points, first = that light) |
| `d_no_occupant` | sealed, no entity at all | 0 | **BSP** — not tested |
| `e_in_solid` | sealed, every entity inside a brush | 0 | **BSP** — the same line as `d` |
| `f_detail_cover` / `f_clip_cover` / `f_window_cover` | the slot covered by a detail, a clip, a translucent window brush | 1 | `.pts`: none seals |
| `g_truncated` | a file cut off mid-plane | 1 | the log only |
| `g_degenerate_plane` | a face whose three points are on one line: the brush is dropped | 1 | `.pts` (6 points) |
| `h_missing_wal` | sealed, one wall naming a texture with no WAL | 0 | BSP — not called sealed |
| `i_stale_pts` | `a_sealed` with a stale `level.pts` planted beforehand | 0 | BSP; qbsp deleted the stale `.pts` (`pts/i_stale_pts.pts` is the planted file) |
| `j_outside_pillars` (`Q2_019`) | `a_sealed` with 764 small pillars in the void around it | 0 | BSP; the fill report reads `[.   ]    1,723 outside leaves` — a progress prefix and a thousands separator, as every real-size map prints it |

## The candidate 1.4522 (`Q2_005B`)

`logs-1.4522/` and `pts-1.4522/` are the same controls run by the fork's candidate 1.4522
(`2.0.0-alpha11-36-g0d64c054+auto-pigeon.1.4522`, commit 0d64c054, built by the fork's CI scripts and NOT
published) with the same command, the same game view (a `baseq2` holding pak0-pak2) and the same job layout;
kept as qbsp printed them (carriage returns included) with only the directory renamed. Every exit, every file
left and every point file is 1.4518's; `TestTheCandidateQbspIsReadAsTheMeasuredRelease` reads each row as the
1.4518 row is read. `pts-1.4522/i_stale_pts.pts` is the planted file, as in `pts/`. The runner is
`LLM/runs/auto-pigeon/Q2_005B/scripts/run_aucom_controls.sh`.

## The release 1.4524 (`Q2_021`)

`logs-1.4524/` and `pts-1.4524/` are the same controls run by release 1.4524
(`2.0.0-alpha11-38-gd4717d68+auto-pigeon.1.4524`, tag `auto-pigeon-ericw-v1.4524`, the published
`auto-pigeon-ericw-tools-1.4524-linux-amd64.zip`, SHA-256 `eb87b62f…` checked against the release's `SHA256SUMS`) with
the same command, game view and job layout. Every exit, file left and point file is 1.4522's; the logs differ from
1.4522's only in the banner and in timings. `TestTheReleasedQbspIsReadAsTheMeasuredRelease` reads each row as the 1.4518
row is read. `pts-1.4524/i_stale_pts.pts` is the planted file. The runner is
`LLM/runs/auto-pigeon/Q2_021/aucom-controls/scripts/run_aucom_controls_1.4524.sh`.
