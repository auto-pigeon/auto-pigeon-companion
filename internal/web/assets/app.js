// Vanilla JS, no framework, no bundler, no npm. Loaded as a classic script from
// the binary's embedded assets — see internal/web/embed.go for why.
//
// Every call goes to the Companion's own loopback server; there are no external requests
// and no third-party scripts, which is what keeps the GUI usable offline and
// keeps an AUB session from being exposed to anything but the Companion itself.

"use strict";

const $ = (id) => document.getElementById(id);

// api posts or gets JSON from the local server and always resolves to
// { ok, status, body } so callers do not have to branch on throw versus reject.
async function api(path, options = {}) {
  const init = { headers: { Accept: "application/json" }, ...options };
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

$("build-button").addEventListener("click", async (event) => {
  await withBusy(event.currentTarget, async () => {
    const output = $("build-output");
    output.textContent = "running…";
    const { ok, body } = await api("/api/build", { method: "POST", body: { tool: "noop", args: [] } });
    output.textContent = ok ? body.output : `${body.output || ""}${body.error}`;
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
    output.textContent = body.started ? `exited: ${body.command}` : `would run: ${body.command}`;
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
