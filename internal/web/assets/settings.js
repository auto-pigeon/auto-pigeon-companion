// The Settings area: where the Companion talks to, and where it keeps things.
//
// The address field is the one control on this page that a first run genuinely
// cannot do without. Nothing in this program has a built-in address for another
// component (see AGENTS.md), so a Companion nobody has configured has nothing to
// fall back on — and a program whose only way to be configured is a text editor
// and a restart is a program most of its users cannot set up at all.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, record } = window.AUCOM;

  async function refresh() {
    const { ok, body } = await api("/api/v1/settings");
    if (!ok) {
      setMessage("settings-message", body.error || "could not read the settings", "error");
      return;
    }
    window.AUCOM.settings = body;

    $("settings-aub").value = body.aub_base_url || "";
    $("settings-port").value = body.port ?? 0;
    $("settings-concurrency").value = body.job_concurrency ?? 0;
    // The field holds what the file holds, so pressing Save never copies an
    // environment variable's value into the file. The note says what is
    // actually in use when the two differ.
    $("settings-aub-note").textContent = body.aub_from_environment
      ? `In use right now: ${body.aub_effective_url}, from the AUCOM_AUB_BASE_URL environment ` +
        `variable, which wins over the configuration file. What you type here is saved to the file ` +
        `and takes effect once that variable is unset.`
      : body.aub_base_url
        ? "Saved in the configuration file."
        : "Not set. Nothing in the Companion has a built-in address for the backend, so this has to be filled in.";
    $("settings-aub").setAttribute("aria-invalid", body.aub_effective_url ? "false" : "true");

    const paths = $("settings-paths");
    paths.replaceChildren();
    const rows = [
      ["Configuration file", body.config_path],
      ["Jobs", body.jobs_dir],
      ["Builds", body.builds_dir],
      ["Profiles", body.profiles_dir],
      ["Local setups", body.bindings_path],
      ["Downloaded assets", body.asset_cache_dir],
      ["Downloaded tools", body.tool_cache_dir],
      ["Acquisition catalogue", body.catalog_url],
      ["Catalogue trust anchors", body.catalog_anchors_path],
      ["File chooser", body.path_helper || "none on this machine — paths are typed"],
      ["Offline mode", body.offline ? "on" : "off"],
    ];
    for (const [name, value] of rows) {
      if (!value) continue;
      paths.append(el("dt", { text: name }));
      paths.append(el("dd", { text: String(value) }));
    }
  }

  async function save(button) {
    await withBusy(button, async () => {
      busy("settings-message", "Saving…");
      const { ok, body } = await api("/api/v1/settings", {
        method: "PUT",
        body: {
          aub_base_url: $("settings-aub").value.trim(),
          port: Number($("settings-port").value || 0),
          job_concurrency: Number($("settings-concurrency").value || 0),
          game_roots: window.AUCOM.settings.game_roots || undefined,
        },
      });
      if (!ok) {
        setMessage("settings-message", body.error || "the settings could not be saved", "error");
        return;
      }
      window.AUCOM.settings = body;
      setMessage(
        "settings-message",
        "Saved. The port takes effect the next time the Companion starts; the backend address is in use now.",
        "ok"
      );
      record("Saved settings", body.aub_base_url || "no backend address", "ok");
      await refresh();
      await window.AUCOM.refreshStatus();
    });
  }

  async function extractorVersion(button) {
    await withBusy(button, async () => {
      const out = $("extractor-output");
      out.textContent = "reading…";
      const { ok, body } = await api("/api/aue/version");
      out.textContent = ok ? body.version : body.error;
    });
  }

  $("settings-save").addEventListener("click", (event) => save(event.currentTarget));
  $("extractor-version").addEventListener("click", (event) => extractorVersion(event.currentTarget));

  window.AUCOM.areas.settings = { refresh };
})();
