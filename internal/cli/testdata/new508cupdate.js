// NEW_508C C2/C3: the sidebar's Update in the real page of a real release, followed
// to its end across the Companion's own restart. The page reloads when the new
// Companion answers; this script is injected again and carries on from
// sessionStorage, sampling once a second what a person would see.

"use strict";

(async () => {
  const KEY = "new508c-driver";
  const report = { sidebar: null, samples: [], error: "" };
  let state;
  try { state = JSON.parse(window.sessionStorage.getItem(KEY) || "null"); } catch { state = null; }
  state = state || { phase: "start", samples: [], sidebar: null, startedAt: 0, loads: 0 };
  state.loads += 1;
  const save = () => window.sessionStorage.setItem(KEY, JSON.stringify(state));
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const $ = (id) => document.getElementById(id);
  const waitFor = async (what, predicate, timeout = 45000) => {
    const deadline = Date.now() + timeout;
    for (;;) {
      const value = predicate();
      if (value) return value;
      if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
      await sleep(50);
    }
  };
  const dialogNamed = (title) => [...document.querySelectorAll("dialog[open]")]
    .find((dialog) => dialog.querySelector("h2")?.textContent === title);
  const sample = () => {
    const modal = $("tools-update-modal");
    const progress = $("tools-update-progress");
    state.samples.push({
      at: Date.now(), load: state.loads,
      open: Boolean(modal && !modal.hidden && progress && !progress.hidden),
      stage: $("tools-update-stage")?.textContent || "",
      indeterminate: !$("tools-update-bar")?.hasAttribute("value"),
      rows: [...document.querySelectorAll("#tools-update-steps [data-state]")].map((row) => row.dataset.tool + ":" + row.dataset.state),
      texts: [...document.querySelectorAll("#tools-update-steps [data-state] span")].map((span) => span.textContent),
      done: Boolean($("tools-update-done") && !$("tools-update-done").hidden),
      result: $("tools-update-result")?.textContent || "",
      stopped: Boolean($("stopped-modal") && !$("stopped-modal").hidden),
    });
    if (state.samples.length > 600) state.samples.splice(1, 1);
    save();
  };

  try {
    if (state.phase === "start") {
      // The first-run Install window opens on its own: Cancel it, as the
      // tester did, and use the sidebar's Update instead.
      const install = await waitFor("the Install window", () => dialogNamed("Install the Auto-Pigeon build tools"), 20000).catch(() => null);
      if (install) [...install.querySelectorAll("button")].find((button) => button.textContent === "Cancel").click();
      const button = await waitFor("the sidebar's Update", () => $("sidebar-update"));
      const nav = button.closest("nav").getBoundingClientRect();
      const box = button.getBoundingClientRect();
      state.sidebar = {
        text: button.textContent, describedBy: $(button.getAttribute("aria-describedby"))?.textContent || "",
        leftInNav: box.left >= nav.left && box.right <= nav.right, belowAreas: box.top > document.querySelector(".areas ul").getBoundingClientRect().bottom,
      };
      state.phase = "following";
      state.startedAt = Date.now();
      save();
      button.click();
    }
    const deadline = state.startedAt + 300000;
    while (Date.now() < deadline) {
      sample();
      const last = state.samples[state.samples.length - 1];
      if (last.done) break;
      await sleep(1000);
    }
    report.sidebar = state.sidebar;
    report.samples = state.samples;
    report.loads = state.loads;
  } catch (error) {
    report.error = error.message;
    report.samples = state.samples;
  }
  await fetch("/journey/report", { method: "POST", body: JSON.stringify(report) });
})();
