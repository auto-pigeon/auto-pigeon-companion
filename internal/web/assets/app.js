// Vanilla JS, no framework, no bundler, no npm. Loaded as a classic script from
// the binary's embedded assets — see internal/web/embed.go for why.
//
// Every call goes to the Companion's own loopback server; there are no external requests
// and no third-party scripts, which is what keeps the GUI usable offline and
// keeps an AUB session from being exposed to anything but the Companion itself.

"use strict";

const $ = (id) => document.getElementById(id);

// The API token for this run, put into the page when it was served. Held in a
// variable and sent as a header: never in localStorage, never in a URL, and
// never a cookie the browser would attach on its own. See internal/web/auth.go.
const apiToken = document.querySelector('meta[name="aucom-api-token"]')?.content || "";

// api posts or gets JSON from the local server and always resolves to
// { ok, status, body } so callers do not have to branch on throw versus reject.
async function api(path, options = {}) {
  const init = { ...options, headers: { Accept: "application/json", "X-AUCOM-Token": apiToken, ...(options.headers || {}) } };
  if (init.body !== undefined && typeof init.body !== "string") {
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(init.body);
  }
  try {
    const response = await fetch(path, init);
    const text = await response.text();
    let body = {};
    if (text) {
      try {
        body = JSON.parse(text);
      } catch {
        body = { error: text };
      }
    }
    return { ok: response.ok, status: response.status, body };
  } catch (err) {
    // A fetch rejection here means the local server went away — the one
    // failure mode a user cannot diagnose from the page itself, so it is said
    // plainly rather than shown as a generic error.
    return { ok: false, status: 0, body: { error: `cannot reach the Companion server: ${err.message}` } };
  }
}

function setMessage(element, text, isError) {
  element.textContent = text || "";
  element.classList.toggle("error", Boolean(isError));
}

async function withBusy(button, work) {
  button.disabled = true;
  try {
    return await work();
  } finally {
    button.disabled = false;
  }
}

async function refreshStatus() {
  const { ok, body } = await api("/api/status");
  if (!ok) {
    $("status-line").textContent = body.error;
    return;
  }
  const who = body.authenticated ? body.email || "signed in" : "not signed in";
  // An unconfigured AUB address comes back empty rather than as a guessed
  // default — the server has no compiled-in one — so the page names the
  // setting the user has to fill in. See internal/config.
  const aub = body.aub_base_url || "AUB not configured (AUCOM_AUB_BASE_URL)";
  $("status-line").textContent = `v${body.version} · ${body.platform} · ${aub} · ${who}`;
  $("logout-button").hidden = !body.authenticated;

  const available = Boolean(body.aue_available);
  $("extractor-state").textContent = available
    ? "The bundled extractor is available."
    : "No extractor binary is bundled in this build. Set AUCOM_AUE_BINARY to a locally built auto-pigeon-extractor to use one.";
  $("extractor-button").disabled = !available;
}

async function refreshLaunchConfigs() {
  const select = $("game");
  const { ok, body } = await api("/api/launch-configs");
  select.innerHTML = "";
  if (!ok) {
    setMessage($("launch-output"), body.error, true);
    return;
  }
  for (const item of body.items || []) {
    const option = document.createElement("option");
    option.value = item.game;
    option.textContent = item.game;
    select.append(option);
  }
}

$("login-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const message = $("auth-message");
  setMessage(message, "signing in…", false);
  const { ok, body } = await api("/api/auth/login", {
    method: "POST",
    body: { email: $("email").value, password: $("password").value },
  });
  // The password field is cleared on every outcome, success or failure: a
  // wrong-password retry should not leave the old value sitting in the DOM.
  $("password").value = "";
  setMessage(message, ok ? body.warning || `signed in as ${body.email}` : body.error, !ok);
  await refreshStatus();
});

$("logout-button").addEventListener("click", async (event) => {
  await withBusy(event.currentTarget, async () => {
    const { ok, body } = await api("/api/auth/logout", { method: "POST" });
    setMessage($("auth-message"), ok ? "signed out" : body.error, !ok);
    await refreshStatus();
  });
});

// --- jobs -------------------------------------------------------------------

let catalog = [];

async function refreshProfiles() {
  const { ok, body } = await api("/api/v1/profiles");
  const profiles = $("job-profile");
  profiles.innerHTML = "";
  if (!ok) {
    setMessage($("job-output"), body.error, true);
    return;
  }
  catalog = body.items || [];
  for (const item of catalog) {
    const option = document.createElement("option");
    option.value = item.id;
    option.textContent = `${item.name} (${item.trust})`;
    profiles.append(option);
  }
  refreshActions();
}

function refreshActions() {
  const actions = $("job-action");
  actions.innerHTML = "";
  const selected = catalog.find((item) => item.id === $("job-profile").value);
  for (const action of selected?.actions || []) {
    const option = document.createElement("option");
    option.value = action.id;
    option.textContent = action.title || action.id;
    actions.append(option);
  }
}

function jobBody() {
  return { profile: $("job-profile").value, action: $("job-action").value };
}

// describeJob is the one renderer for a job record, so the preview, the run and
// the list cannot disagree about what a job's state is called.
function describeJob(job) {
  const parts = [`${job.id}  ${job.state}`];
  if (job.command) parts.push(`  ${job.command.shell}`);
  if (job.exit_code !== undefined) parts.push(`  exit status ${job.exit_code}`);
  if (job.error) parts.push(`  ${job.error}`);
  for (const artifact of job.artifacts || []) {
    if (!artifact.missing) parts.push(`  artifact ${artifact.name}: ${artifact.path}`);
  }
  return parts.join("\n");
}

async function refreshJobs() {
  const { ok, body } = await api("/api/v1/jobs?limit=10");
  const list = $("job-list");
  list.innerHTML = "";
  if (!ok) {
    setMessage($("job-output"), body.error, true);
    return;
  }
  for (const job of body.items || []) {
    const entry = document.createElement("li");
    entry.textContent = `${job.id}  ${job.state}  ${job.profile_id || ""} ${job.action_id || ""}`;
    list.append(entry);
  }
}

$("job-profile").addEventListener("change", refreshActions);
$("job-refresh-button").addEventListener("click", (event) => withBusy(event.currentTarget, refreshJobs));

$("job-preview-button").addEventListener("click", async (event) => {
  await withBusy(event.currentTarget, async () => {
    const output = $("job-output");
    output.textContent = "resolving…";
    const { ok, body } = await api("/api/v1/jobs/preview", { method: "POST", body: jobBody() });
    output.textContent = ok ? describeJob(body) : body.error;
  });
});

$("job-run-button").addEventListener("click", async (event) => {
  await withBusy(event.currentTarget, async () => {
    const output = $("job-output");
    output.textContent = "starting…";
    const submitted = await api("/api/v1/jobs", { method: "POST", body: jobBody() });
    if (!submitted.ok) {
      output.textContent = submitted.body.error;
      return;
    }
    // Polled rather than streamed: the record is the truth about a job, and a
    // page that read a stream would be reading something else.
    const id = submitted.body.id;
    for (;;) {
      const { ok, body } = await api(`/api/v1/jobs/${id}`);
      if (!ok) {
        output.textContent = body.error;
        return;
      }
      output.textContent = describeJob(body);
      if (["succeeded", "failed", "cancelled", "interrupted"].includes(body.state)) break;
      await new Promise((resolve) => setTimeout(resolve, 400));
    }
    const logs = await api(`/api/v1/jobs/${id}/logs`);
    if (logs.ok && logs.body.text) {
      output.textContent += `\n\n${logs.body.text}`;
    }
    await refreshJobs();
  });
});

async function launch(dryRun, button) {
  await withBusy(button, async () => {
    const output = $("launch-output");
    output.textContent = dryRun ? "resolving…" : "launching…";
    const { ok, body } = await api("/api/launch", {
      method: "POST",
      body: {
        game: $("game").value,
        map: $("map").value,
        game_root: $("game-root").value,
        dry_run: dryRun,
      },
    });
    if (!ok) {
      output.textContent = body.error;
      return;
    }
    output.textContent = body.started
      ? `started as job ${body.job.id}`
      : `would run: ${body.command}`;
  });
}

$("extractor-button").addEventListener("click", async (event) => {
  await withBusy(event.currentTarget, async () => {
    const output = $("extractor-output");
    output.textContent = "reading…";
    const { ok, body } = await api("/api/aue/version");
    output.textContent = ok ? body.version : body.error;
  });
});

$("preview-button").addEventListener("click", (event) => launch(true, event.currentTarget));
$("launch-button").addEventListener("click", (event) => launch(false, event.currentTarget));

refreshStatus();
refreshLaunchConfigs();
refreshProfiles();
refreshJobs();
