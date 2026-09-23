// Stack frames for GlitchTip's source-map symbolication — and nothing else from a stack.
//
// ## Why this exists
//
// The envelope has no stack field, on purpose: a stack string carries URLs, hosts, query strings and
// sometimes local paths, and the envelope is closed. But a production browser bundle is minified,
// and an operator reading "render.3d.fatal" needs the source line. GlitchTip symbolicates a frame by
// the BASENAME of its `abs_path` against the maps uploaded privately for that exact release
// (auto-pigeon-telemetry/scripts/upload-sourcemaps.py), so a frame needs only four things: the
// bundle file's path, the function name, the line and the column.
//
// ## What is kept, and what can never be
//
// Only frames whose script lives on the application's OWN origin, under a path made of plain
// file-name characters and ending `.js`/`.mjs`. The origin is dropped and replaced with `app://`, so
// no host, IP, port, query or fragment ever leaves the browser: `https://192.168.0.33:5173/assets/
// index-C9.js?v=1` becomes `app:///assets/index-C9.js`. Frames from extensions, other origins,
// `blob:`, `data:` and `eval` are dropped, not masked. A function name that is not a plain
// identifier becomes `?`. At most 30 frames, innermost last, as Sentry expects.
//
// These are added to the event AFTER redaction, deliberately: `app:///…` is not user data, and the
// `url` redaction rule would otherwise erase the one field symbolication needs.

const MAX_FRAMES = 30;
const PATH = /^\/(?:[A-Za-z0-9._-]+\/){0,8}[A-Za-z0-9._-]+\.m?js$/;
const FUNCTION = /^[A-Za-z0-9_$.<>]{1,80}$/;
const TYPE = /^[A-Z][A-Za-z0-9]{0,39}$/;
// Chromium: "    at fn (URL:1:2)" / "    at URL:1:2". Firefox/Safari: "fn@URL:1:2" / "@URL:1:2".
const V8 = /^\s*at (?:(.+?) \()?(.+?):(\d+):(\d+)\)?\s*$/;
const GECKO = /^\s*(.*?)@(.+?):(\d+):(\d+)\s*$/;

function frameFrom(line, origin) {
  const match = V8.exec(line) ?? GECKO.exec(line);
  if (!match) return undefined;
  const [, rawFunction, location, lineNo, colNo] = match;
  if (typeof origin !== "string" || !origin || !location.startsWith(`${origin}/`)) return undefined;
  const path = location.slice(origin.length).split(/[?#]/)[0];
  if (!PATH.test(path)) return undefined;
  const name = (rawFunction ?? "").replace(/^async /, "").trim();
  const lineno = Number(lineNo);
  const colno = Number(colNo);
  if (!Number.isInteger(lineno) || !Number.isInteger(colno) || lineno < 1 || colno < 1) return undefined;
  return {
    abs_path: `app://${path}`,
    filename: `app://${path}`,
    function: FUNCTION.test(name) ? name : "?",
    lineno,
    colno,
    in_app: true,
  };
}

/**
 * Sanitised frames from an `Error.stack` string, for `toSentryEvent(incident, { exception })`.
 * `origin` is the page's own origin (`location.origin`). Returns `[]` when nothing qualifies.
 */
export function stackFrames(stack, { origin } = {}) {
  if (typeof stack !== "string" || !stack) return [];
  const frames = [];
  for (const line of stack.split("\n").slice(0, 200)) {
    const frame = frameFrom(line, origin);
    if (frame) frames.push(frame);
    if (frames.length >= MAX_FRAMES) break;
  }
  return frames.reverse();
}

/** The exception type Sentry groups and displays, kept only when it is a plain constructor name. */
export function exceptionType(error) {
  const name = error && typeof error === "object" ? error.name : undefined;
  return typeof name === "string" && TYPE.test(name) ? name : "Error";
}
