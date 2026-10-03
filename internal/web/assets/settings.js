// The Settings area: where the Companion talks to, and where it keeps things.
//
// The address field is the one control on this page that a first run genuinely
// cannot do without. Nothing in this program has a built-in address for another
// component (see AGENTS.md), so a Companion nobody has configured has nothing to
// fall back on — and a program whose only way to be configured is a text editor
// and a restart is a program most of its users cannot set up at all.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, record, t } = window.AUCOM;

  // Whether this computer hands `autopigeon://` links to this Companion, and
  // the button that makes it so (NEW_307W). The sentence under the state is
  // the registrar's own: which key or file, or why this platform registers
  // another way.
  async function refreshLinks() {
    const { ok, body } = await api("/api/v1/uri");
    if (!ok) {
      $("uri-state").textContent = body.error || t("The link handler could not be read.");
      $("uri-register").hidden = true;
      return;
    }
    $("uri-state").textContent = body.registered
      ? t("This Companion opens Auto-Pigeon links on this computer.")
      : ((body.command || []).length
          // A handler exists and runs another program — an older Companion.
          ? t("Auto-Pigeon links open another program on this computer, not this Companion, so the editor's Test in Companion and join links do not arrive here.")
          : t("Nothing on this computer opens Auto-Pigeon links yet, so the editor's Test in Companion and join links do nothing.")) +
        (body.detail ? " " + body.detail : "");
    $("uri-register").hidden = Boolean(body.registered);
  }

  $("uri-register").addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
    const { ok, body } = await api("/api/v1/uri/register", { method: "POST" });
    if (!ok) {
      setMessage("uri-message", body.error || t("The link handler could not be registered."), "error");
      return;
    }
    setMessage("uri-message", body.registered ? "" : body.detail || "", body.registered ? "" : "error");
    if (body.registered) record(t("Auto-Pigeon links now open in this Companion"), "", "ok");
    await refreshLinks();
  }));

  async function refresh() {
    refreshLinks();
    const { ok, body } = await api("/api/v1/settings");
    if (!ok) {
      setMessage("settings-message", body.error || "could not read the settings", "error");
      return;
    }
    window.AUCOM.settings = body;

    $("settings-aub").value = body.aub_base_url || "";
    // A person chooses one of the official servers. Typing an address is a
    // developer's act: the field exists only with --debug (HITL, NEW_244D).
    window.AUCOM.renderBackendChoice($("settings-backend"), {
      backends: body.backends, aub_base_url: body.aub_base_url, debug: body.debug,
    });
    $("settings-aub-field").hidden = !body.debug;
    $("settings-port").value = body.port ?? 0;
    $("settings-concurrency").value = body.job_concurrency ?? 0;
    // The field holds what the file holds, so pressing Save never copies an
    // environment variable's value into the file. The note says what is
    // actually in use when the two differ.
    const ignored = (body.aub_ignored || []).length
      ? ` Ignored: ${body.aub_ignored.join("; ")}. ${body.aub_ignored_why}`
      : "";
    $("settings-aub-note").textContent = (body.aub_from_environment
      ? `In use right now: ${body.aub_effective_url}, set when the Companion was started ` +
        `(environment, .env or the config.json beside the program). It wins over what is chosen here.`
      : body.aub_base_url
        ? "Saved. Signing in and My Maps use this server."
        : "No server chosen. Nothing is contacted until you choose one; everything local works without it.") + ignored;

    const paths = $("settings-paths");
    paths.replaceChildren();
    const rows = [
      ["Configuration file", body.config_path],
      ["Jobs", body.jobs_dir],
      ["Builds", body.builds_dir],
      ["Profiles", body.profiles_dir],
      ["Local setups", body.bindings_path],
      ["Downloaded assets", body.asset_cache_dir],
      ["File chooser", body.path_helper || "none on this machine — paths are typed"],
      ["Offline mode", body.offline ? "on" : "off"],
    ];
    for (const [name, value] of rows) {
      if (!value) continue;
      paths.append(el("dt", { text: name }));
      paths.append(el("dd", { text: String(value) }));
    }
  }

  const body_debug = () => Boolean(window.AUCOM.settings && window.AUCOM.settings.debug);

  $("settings-backend").addEventListener("change", () => {
    if (body_debug()) $("settings-aub").value = $("settings-backend").value;
  });

  async function save(button) {
    await withBusy(button, async () => {
      busy("settings-message", "Saving…");
      const { ok, body } = await api("/api/v1/settings", {
        method: "PUT",
        body: {
          aub_base_url: (body_debug() ? $("settings-aub").value.trim() : "") || $("settings-backend").value,
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
        "Saved. The port takes effect the next time the Companion starts; the server choice is in use now.",
        "ok"
      );
      record("Saved settings", body.aub_base_url || "no server chosen", "ok");
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
