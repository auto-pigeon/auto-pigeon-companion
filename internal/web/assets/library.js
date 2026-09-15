// The Library area: what is on the backend, and what is on this machine.
//
// The two lists stay separate all the way down to the DOM. A merged one would
// have to invent a state for "listed but not here", and the moment a build
// depended on that state it would be a build that fetched something the user
// thought they already had.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, record, badge, bytes, when } = window.AUCOM;

  let assets = [];
  let cachedKeys = new Set();

  // chosen is the revision the Build area will offer as an input. One at a
  // time, and named exactly — never the word `current`, which is a question
  // rather than an answer.
  window.AUCOM.chosenRevision = null;

  // typeName says an asset type the way a person would: `prefab_package` is
  // the account's own word for a thing people call a prefab package.
  const TYPE_NAMES = {
    map: ["Map", "Maps"], prefab_package: ["Prefab package", "Prefab packages"],
    texture_source: ["Texture source", "Texture sources"], game_profile: ["Game profile", "Game profiles"],
    entity_catalogue: ["Entity catalogue", "Entity catalogues"],
  };
  function typeName(type, plural = false) {
    const known = TYPE_NAMES[type];
    if (known) return known[plural ? 1 : 0];
    const words = String(type || "asset").replace(/_/g, " ");
    return words.charAt(0).toUpperCase() + words.slice(1) + (plural ? "s" : "");
  }
  window.AUCOM.typeName = typeName;

  async function refreshTypes() {
    const select = $("library-type");
    if (select.options.length > 1) return;
    const { ok, body } = await api("/api/v1/library/capabilities");
    if (!ok) return;
    for (const type of body.asset_types || []) {
      select.append(el("option", { text: typeName(type.asset_type, true), attrs: { value: type.asset_type } }));
    }
  }

  async function refreshCatalog() {
    const list = $("library-assets");
    busy("library-message", "Reading your account…");
    const query = new URLSearchParams();
    const type = $("library-type").value;
    const name = $("library-search").value.trim();
    if (type) query.set("type", type);
    if (name) query.set("name", name);
    query.set("limit", "50");

    const { ok, status, body } = await api("/api/v1/library/catalog?" + query.toString());
    list.replaceChildren();
    if (!ok) {
      // The exact cause, and the thing to do about it. A 401 here means the
      // session, not the password field the user is looking at.
      const hint =
        status === 401
          ? " Sign in again with the Sign in button at the top of the page."
          : status === 503
            ? " Choose your Auto-Pigeon server in Settings."
            : "";
      setMessage("library-message", (body.error || "could not read the catalogue") + hint, "error");
      return;
    }
    assets = body.items || [];
    if (assets.length === 0) {
      setMessage("library-message", "Nothing matched. This account may have no assets of that type yet.");
      return;
    }
    setMessage("library-message", `${assets.length} asset${assets.length === 1 ? "" : "s"}.`);
    for (const asset of assets) list.append(assetCard(asset));
  }

  // A person knows a map by the name they gave it. Ids, revision ids and cache
  // keys are the program's own bookkeeping and are never shown (operator,
  // NEW_244D). The ordinary action is the LATEST revision; an older one is
  // behind "Other revisions", for the rare time somebody needs it.
  function nameOf(asset) {
    return asset.display_name || "Untitled " + typeName(asset.asset_type).toLowerCase();
  }

  function assetCard(asset) {
    const title = el("h4", { text: nameOf(asset) });
    const meta = el("p", { className: "muted" });
    meta.textContent = [typeName(asset.asset_type), asset.game, asset.visibility].filter(Boolean).join(" · ");

    const revision = asset.current_revision;
    const current = el("p", { className: "muted" });
    current.textContent = revision
      ? `Latest: revision ${revision.revision}${revision.created_at ? " · " + when(revision.created_at) : ""}`
      : "Nothing saved yet";

    const status = el("p", { className: "message", attrs: { role: "status" } });
    const download = el("button", { text: "Download latest", attrs: { type: "button", class: "primary" } });
    download.disabled = !revision;
    download.addEventListener("click", () =>
      withBusy(download, () => downloadRevision(asset, revision || {}, "current", status, download))
    );
    // An older revision is a rare need, so it is folded away: the card offers
    // the latest, and the fold offers the rest.
    const other = el("button", { text: "Choose an older revision…", attrs: { type: "button", class: "secondary" } });
    other.addEventListener("click", () => openRevisions(asset, other));
    const more = el("details", {
      className: "more",
      children: [el("summary", { text: "More" }), other],
    });

    return el("li", {
      className: "card",
      children: [title, meta, current, el("div", { className: "row-actions", children: [download, more] }), status],
    });
  }

  // downloadRevision fetches and verifies one revision — the latest unless
  // somebody chose another — and offers it to the Build area.
  async function downloadRevision(asset, revision, key, status, button) {
    busy(status, "Downloading and verifying…");
    const { ok, body } = await api("/api/v1/library/sync", {
      method: "POST",
      body: { asset_type: asset.asset_type, asset_id: asset.asset_id, revision: key || "current" },
    });
    if (!ok) {
      setMessage(status, body.error || "the download failed", "error");
      record(`Download of ${nameOf(asset)} failed`, body.error, "failed");
      return false;
    }
    const summary = body.already_complete
      ? "Already on this computer. It is ready to build."
      : `Downloaded (${bytes(body.bytes_fetched)}). It is ready to build.`;
    setMessage(status, summary, "ok");
    cachedKeys.add(body.key);
    if (button) button.textContent = "Download again";
    record(`Downloaded ${nameOf(asset)}, revision ${body.record.revision}`, "", "ok");
    await refreshCached();
    chooseRevision(asset, body.record, body.key);
    return true;
  }

  async function openRevisions(asset, trigger) {
    await withBusy(trigger, async () => {
      const panel = $("library-revisions-panel");
      const list = $("library-revisions");
      panel.hidden = false;
      $("library-revisions-title").textContent = "Revisions of " + nameOf(asset);
      list.replaceChildren(el("li", { className: "muted", text: "Reading…" }));

      const { ok, body } = await api(
        `/api/v1/library/assets/${encodeURIComponent(asset.asset_type)}/${encodeURIComponent(asset.asset_id)}`
      );
      list.replaceChildren();
      if (!ok) {
        setMessage("library-revisions-note", body.error || "could not read this asset", "error");
        return;
      }
      cachedKeys = new Set(body.cached || []);
      const revisions = body.revisions || [];
      $("library-revisions-note").textContent = body.history_error
        ? "This asset type keeps no per-version history: " + body.history_error
        : `${revisions.length} revision${revisions.length === 1 ? "" : "s"}. ` +
          "Downloading one verifies every file against the digest the server declared.";
      $("library-revisions-note").className = "muted";

      const rows = revisions.length > 0 ? revisions : body.asset?.current_revision ? [body.asset.current_revision] : [];
      if (rows.length === 0) {
        list.append(el("li", { className: "muted", text: "This asset has no revisions yet." }));
        return;
      }
      for (const revision of rows) list.append(revisionRow(asset, revision));
      // Focus moves into the panel that just appeared, so a keyboard user is
      // where the new content is rather than back at the button they pressed.
      $("library-revisions-title").setAttribute("tabindex", "-1");
      $("library-revisions-title").focus();
    });
  }

  function revisionRow(asset, revision) {
    const key = revision.revision_id || "";
    const held = key && cachedKeys.has(key);

    const head = el("div", { className: "row-head" });
    const label = el("strong", { text: `Revision ${revision.revision}` });
    head.append(label);
    head.append(badge(held ? "downloaded" : "in your account", held ? "ok" : "queued"));

    const detail = el("p", { className: "muted" });
    detail.textContent = [
      revision.created_at ? "saved " + when(revision.created_at) : "",
      revision.immutable ? "" : "may still change",
    ]
      .filter(Boolean)
      .join(" · ");

    const download = el("button", { text: held ? "Download again" : "Download", attrs: { type: "button" } });
    const use = el("button", { text: "Use in a build", attrs: { type: "button", class: "secondary" } });
    use.disabled = !held;

    const status = el("p", { className: "message", attrs: { role: "status" } });

    download.addEventListener("click", () =>
      withBusy(download, async () => {
        if (await downloadRevision(asset, revision, key, status, download)) use.disabled = false;
      })
    );

    use.addEventListener("click", () => {
      chooseRevision(asset, revision, key);
      setMessage(status, "Chosen. The Build area will offer it as an input.", "ok");
    });

    return el("li", {
      children: [head, detail, el("div", { className: "row-actions", children: [download, use] }), status],
    });
  }

  function chooseRevision(asset, revision, key) {
    window.AUCOM.chosenRevision = {
      asset_type: asset.asset_type,
      asset_id: asset.asset_id,
      display_name: nameOf(asset),
      revision_id: key || revision.revision_id || "",
      revision: revision.revision,
      files: revision.files || [],
    };
    window.AUCOM.areas.build?.revisionChosen?.();
  }

  async function refreshCached() {
    const list = $("cached-list");
    const { ok, body } = await api("/api/v1/library/cached");
    list.replaceChildren();
    if (!ok) {
      setMessage("cached-message", body.error || "could not read the local cache", "error");
      return;
    }
    const items = body.items || [];
    setMessage(
      "cached-message",
      items.length === 0
        ? "Nothing downloaded yet."
        : `${items.length} downloaded to this computer.`
    );
    for (const item of items) {
      const record_ = item.record;
      const head = el("div", { className: "row-head" });
      // A record made before the name was known still gets one from the
      // account's own listing, when this page has read it.
      const known = assets.find((asset) => asset.asset_id === record_.asset_id);
      const name = record_.display_name || known?.display_name || "Untitled " + typeName(record_.asset_type).toLowerCase();
      head.append(el("strong", { text: name }));
      head.append(badge(typeName(record_.asset_type), "ok"));
      const detail = el("p", { className: "muted" });
      detail.textContent = [
        `revision ${record_.revision}`,
        bytes(record_.total_bytes),
        "downloaded " + when(record_.synced_at),
      ]
        .filter(Boolean)
        .join(" · ");

      const use = el("button", { text: "Use in a build", attrs: { type: "button", class: "secondary" } });
      use.addEventListener("click", () => {
        chooseRevision(
          { asset_type: record_.asset_type, asset_id: record_.asset_id, display_name: name },
          record_,
          item.key
        );
        setMessage("cached-message", "Chosen. The Build area will offer it as an input.", "ok");
      });
      list.append(el("li", { children: [head, detail, el("div", { className: "row-actions", children: [use] })] }));
    }
  }

  $("library-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshCatalog));
  $("cached-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshCached));
  $("library-type").addEventListener("change", refreshCatalog);
  $("library-search").addEventListener("change", refreshCatalog);
  $("library-close-revisions").addEventListener("click", () => {
    $("library-revisions-panel").hidden = true;
  });

  window.AUCOM.areas.library = {
    async refresh() {
      // The cache first: it needs no session and no network, so a signed-out or
      // offline Companion still shows something true straight away.
      await refreshCached();
      if (window.AUCOM.status.authenticated) {
        await refreshTypes();
        await refreshCatalog();
      } else {
        setMessage("library-message", "Sign in to see what is in your account.", "");
        $("library-assets").replaceChildren();
      }
    },
  };
})();
