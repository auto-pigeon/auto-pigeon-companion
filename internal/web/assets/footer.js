// The frame round the areas: the cog menu (Settings and Language), the "what
// is sync?" help, and the footer — About, News, Report a bug, and the version.
//
// News is a page on the gallery. The Companion has no built-in address for the
// gallery (no component compiles in where another lives), so the address comes
// from the Auto-Pigeon server, through /api/v1/site-links. When the server has
// not said, the link says so instead of guessing a host.

"use strict";

(() => {
  const { $, el, api, t } = window.AUCOM;
  const I18N = window.AUCOM_I18N;

  // --- menus that open from a header button -----------------------------------

  // One behaviour for every small popup here: the button toggles it, a press
  // outside or Escape closes it, and closing returns focus to the button.
  function popup(button, panel, { onOpen } = {}) {
    const open = () => {
      panel.hidden = false;
      button.setAttribute("aria-expanded", "true");
      onOpen?.();
    };
    const close = ({ focus = false } = {}) => {
      if (panel.hidden) return;
      panel.hidden = true;
      button.setAttribute("aria-expanded", "false");
      if (focus) button.focus();
    };
    button.addEventListener("click", () => (panel.hidden ? open() : close()));
    document.addEventListener("pointerdown", (event) => {
      if (!panel.hidden && !panel.contains(event.target) && !button.contains(event.target)) close();
    });
    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape" && !panel.hidden) close({ focus: true });
    });
    return { open, close };
  }

  const settingsMenu = popup($("settings-button"), $("settings-menu"));
  popup($("sync-help-button"), $("sync-help-panel"));
  const userMenu = popup($("user-button"), $("user-menu"));
  $("user-sign-in").addEventListener("click", () => {
    userMenu.close();
    window.AUCOM.openSignIn($("user-button"));
  });
  $("sign-out").addEventListener("click", () => userMenu.close());

  $("settings-open").addEventListener("click", () => {
    settingsMenu.close();
    window.AUCOM.showArea("settings");
  });

  // --- language ---------------------------------------------------------------

  function drawLanguages(saved) {
    const select = $("language-select");
    const detected = I18N.LANGUAGES.find((item) => item.code === I18N.preferredLanguage());
    select.replaceChildren(el("option", {
      text: t("Automatic — {language}", { language: detected?.label || "English" }),
      attrs: { value: "" },
    }));
    for (const item of I18N.LANGUAGES) {
      select.append(el("option", {
        text: item.translated ? item.label : t("{language} — not translated yet", { language: item.label }),
        attrs: { value: item.code, lang: item.code, class: "no-i18n" },
      }));
    }
    select.value = saved || "";
  }

  async function chooseLanguage(code) {
    I18N.setLanguage(code || I18N.preferredLanguage());
    drawLanguages(code);
    window.AUCOM.settings.language = code;
    const { ok, body } = await api("/api/v1/settings/language", { method: "PUT", body: { language: code } });
    if (ok) window.AUCOM.settings = body;
  }

  $("language-select").addEventListener("change", (event) => chooseLanguage(event.target.value));

  // Called once the settings have arrived (app.js), so the page is drawn in
  // the saved language from its first paint of real content.
  function applySavedLanguage() {
    const saved = window.AUCOM.settings?.language || "";
    I18N.setLanguage(saved || I18N.preferredLanguage());
    drawLanguages(saved);
  }

  // --- the footer ---------------------------------------------------------------

  function statusChanged(status) {
    // The version only in its frozen `1.<commit-count>` shape, as AUG prints
    // it; an unstamped build's "unknown" is not a version to read out.
    const version = /^1\.\d+$/.test(status.version || "") ? t("Version {version}", { version: status.version }) : "";
    const site = window.AUCOM.officialSite?.(status);
    const backend = site ? site.host : status.aub_base_url ? t("development server") : t("no Auto-Pigeon server chosen");
    const where = [status.platform, backend].filter(Boolean).join(" · ") + (status.debug ? " · " + t("debug mode") : "");
    const identity = $("identity");
    // Platform and server first, then the version, bottom right (operator,
    // 2026-09-22): "linux/amd64 · development server | Version 1.124".
    identity.replaceChildren(el("span", { text: where }));
    if (version) {
      identity.append(el("span", { className: "site-footer__sep", text: "|", attrs: { "aria-hidden": "true" } }));
      identity.append(el("span", { className: "site-footer__ver", text: version }));
    }
    $("about-version").textContent = version ? `${version} · ${status.platform || ""}` : status.platform || "";

    // Asked again only when the server in use changes.
    if (status.aub_base_url !== linksFor) {
      linksFor = status.aub_base_url;
      refreshSiteLinks();
    }
  }

  // News lives on the gallery, at the address the server gave out.
  let linksFor;
  async function refreshSiteLinks() {
    const { ok, body } = await api("/api/v1/site-links");
    const gallery = ok ? String(body.gallery_url || "").replace(/\/+$/, "") : "";
    $("news-link").hidden = !gallery;
    $("news-unavailable").hidden = Boolean(gallery);
    if (gallery) $("news-link").href = gallery + "/news";
  }

  function modal(dialog, closeButton) {
    closeButton?.addEventListener("click", () => dialog.close());
    // A press on the backdrop (the dialog element itself, outside its content)
    // closes it, as AUG's dialogs do.
    dialog.addEventListener("click", (event) => {
      if (event.target !== dialog) return;
      const box = dialog.getBoundingClientRect();
      const inside = event.clientX >= box.left && event.clientX <= box.right &&
        event.clientY >= box.top && event.clientY <= box.bottom;
      if (!inside) dialog.close();
    });
    return () => dialog.showModal();
  }

  const openAbout = modal($("about-dialog"), $("about-close"));
  $("about-open").addEventListener("click", openAbout);

  const openNewsReason = modal($("news-dialog"), $("news-close"));
  $("news-unavailable").addEventListener("click", () => {
    const status = window.AUCOM.status || {};
    $("news-reason").textContent = !status.aub_base_url
      ? t("News is published on the Auto-Pigeon web site. Choose which Auto-Pigeon server you use in Settings, and the link will point there.")
      : t("News is published on the Auto-Pigeon web site, and the Auto-Pigeon server this Companion uses has not said where that is. Its operator sets the gallery address (AUB_GALLERY_PUBLIC_URL).");
    openNewsReason();
  });

  Object.assign(window.AUCOM, {
    applySavedLanguage,
    footer: { statusChanged },
  });
})();
