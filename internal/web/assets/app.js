// Vanilla JS, no bundler, no framework, no npm. The file is served straight
// out of the Go binary, so "rebuild the frontend" is just `go build`.
//
// Every DOM write below goes through textContent, never innerHTML: values
// arriving from AUB (an email address, an error message) are untrusted text
// and must never be parsed as markup.

const api = {
  async get(path) {
    return unwrap(await fetch(path, { headers: { Accept: "application/json" } }));
  },
  async post(path, body) {
    return unwrap(
      await fetch(path, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: body === undefined ? undefined : JSON.stringify(body),
      }),
    );
  },
};

async function unwrap(response) {
  if (response.status === 204) return null;
  let payload = null;
  try {
    payload = await response.json();
  } catch {
    // Fall through: a non-JSON body on an error status is still an error.
  }
  if (!response.ok) {
    throw new Error((payload && payload.error) || `request failed (${response.status})`);
  }
  return payload;
}

const el = (id) => document.getElementById(id);

function banner(message, isError) {
  const node = el("banner");
  node.textContent = message;
  node.classList.toggle("error", Boolean(isError));
  node.hidden = !message;
}

function renderStatus(status) {
  el("version").textContent = status.version ? `v${status.version}` : "";
  el("aub-url").textContent = status.aub_base_url || "(not configured)";

  el("login-form").hidden = status.authenticated;
  el("signed-in").hidden = !status.authenticated;
  el("account-email").textContent = status.email || "";

  el("aue-state").textContent = status.aue_available
    ? "The bundled extractor is available."
    : "No extractor binary is bundled in this build. Set AUC_AUE_BINARY to a local auto-pigeon-extractor to use one.";
  el("aue-version-button").disabled = !status.aue_available;
}

async function refresh() {
  try {
    renderStatus(await api.get("/api/status"));
  } catch (error) {
    banner(error.message, true);
  }
}

async function withButton(button, work) {
  button.disabled = true;
  try {
    await work();
  } catch (error) {
    banner(error.message, true);
  } finally {
    button.disabled = false;
  }
}

el("login-form").addEventListener("submit", (event) => {
  event.preventDefault();
  banner("");
  withButton(el("login-button"), async () => {
    const status = await api.post("/api/auth/login", {
      identity: el("identity").value,
      password: el("password").value,
    });
    el("password").value = "";
    renderStatus(status);
    banner(`Signed in as ${status.email || status.aub_base_url}.`, false);
  });
});

el("logout-button").addEventListener("click", () => {
  banner("");
  withButton(el("logout-button"), async () => {
    await api.post("/api/auth/logout");
    await refresh();
    banner("Signed out.", false);
  });
});

el("aue-version-button").addEventListener("click", () => {
  banner("");
  withButton(el("aue-version-button"), async () => {
    const result = await api.get("/api/aue/version");
    const output = el("aue-output");
    output.textContent = result.version;
    output.hidden = false;
  });
});

refresh();
