// NEW_365 (HITL review finding 3): a review of a file build is a statement
// about the setup it was made against. The two functions that decide whether
// it still is are lifted out of build.js and run on the pipeline listings the
// page receives before and after a trip to Profiles.

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../assets/build.js", import.meta.url), "utf8");
const start = source.indexOf("  function setupKey(pipeline)");
const end = source.indexOf("  // forgetReview drops a review");
assert.ok(start > 0 && end > start, "build.js no longer defines setupKey/reviewStale where this check looks");
const { setupKey, reviewStale } = new Function(source.slice(start, end) + "\nreturn { setupKey, reviewStale };")();

const blocked = {
  id: "local.pipeline.q1", version: "1.0.0", digest: "sha256:aa",
  steps: [
    { id: "compile", provider: { id: "local.tool.ericw", version: "1.4535" }, provider_version: "1.4533", provider_authorized: true, provider_bound: true },
    { id: "light", provider: { id: "local.tool.ericw", version: "1.4535" }, provider_version: "1.4533", provider_authorized: true, provider_bound: false },
  ],
  readiness: { ready: false, problems: [{ summary: "light not set up yet" }] },
};
// The same profile after light was set up in Profiles.
const repaired = structuredClone(blocked);
repaired.steps[1].provider_bound = true;
repaired.readiness = { ready: true, problems: [] };

const reviewed = setupKey(blocked);
assert.equal(reviewStale(reviewed, structuredClone(blocked)), false, "the same setup re-read is still the reviewed one");
assert.equal(reviewStale(reviewed, repaired), true, "a program set up since the review makes the review stale");

// A tool rebound to another version, still ready: stale.
const rebound = structuredClone(repaired);
rebound.steps[0].provider_version = "1.4502";
assert.equal(reviewStale(setupKey(repaired), rebound), true, "a rebound tool makes the review stale");
// An approval revoked: stale.
const revoked = structuredClone(repaired);
revoked.steps[0].provider_authorized = false;
assert.equal(reviewStale(setupKey(repaired), revoked), true, "a revoked approval makes the review stale");
// Another build profile chosen: stale; nothing reviewed yet: never stale.
assert.equal(reviewStale(reviewed, { ...repaired, id: "other" }), true);
assert.equal(reviewStale("", repaired), false);

// And the page no longer checks on the person's behalf when it comes back.
const refresh = source.slice(source.indexOf("    async refresh() {"), source.indexOf("    showStep,\n  };"));
assert.ok(!/preview\(\$\("build-start"\)\)/.test(refresh), "refresh() must not run a check nobody asked for");
assert.ok(source.includes("if (checked !== null && reviewStale(reviewedSetup, currentPipeline())) forgetReview();"),
  "refreshPipelines must drop a review whose setup changed");
console.log("build review: a check is dropped when the setup under it changes, and refresh() starts none itself");
