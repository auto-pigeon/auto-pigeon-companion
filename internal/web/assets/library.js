// The Library area: what is on the backend, and what is on this machine.
//
// The two lists stay separate all the way down to the DOM. A merged one would
// have to invent a state for "listed but not here", and the moment a build
// depended on that state it would be a build that fetched something the user
// thought they already had.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, record, badge, bytes, when, t } = window.AUCOM;

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


  // The account is read a page at a time. The first page used to be all the
  // page ever showed: with more than 50 assets, dm2 was simply absent until its
  // name was typed into the filter, under a message that said "50 assets."
  // as if that were all of them (NEW_244D). The rest is behind Show more.
  let nextCursor = "";

  function catalogQuery(cursor) {
    // Maps only (operator, 2026-09-22). Texture sources, entity catalogues
    // and the rest are the editor's business; a build fetches the textures a
    // map needs on its own.
    const query = new URLSearchParams();
    const name = $("library-search").value.trim();
    query.set("type", "map");
    if (name) query.set("name", name);
    query.set("limit", "50");
    if (cursor) query.set("cursor", cursor);
    return query;
  }

  function sayHowMany(hasMore) {
    nextCursor = hasMore ? nextCursor : "";
    $("library-more").hidden = !hasMore;
    setMessage("library-message", hasMore
      ? t("Showing the first {n}. Your account has more: press Show more, or find a map by name.", { n: assets.length })
      : assets.length === 1 ? t("1 map.") : t("{n} maps.", { n: assets.length }));
  }

  async function showMore(button) {
    if (!nextCursor) return;
    await withBusy(button, async () => {
      const { ok, body } = await api("/api/v1/library/catalog?" + catalogQuery(nextCursor).toString());
      if (!ok) {
        setMessage("library-message", body.error || "could not read the next page", "error");
        return;
      }
      const more = body.items || [];
      assets = assets.concat(more);
      for (const asset of more) $("library-assets").append(assetCard(asset));
      nextCursor = body.next_cursor || "";
      sayHowMany(Boolean(body.has_more && nextCursor));
    });
  }

  async function refreshCatalog() {
    const list = $("library-assets");
    busy("library-message", "Reading your account…");
    $("library-more").hidden = true;
    nextCursor = "";
    const query = catalogQuery("");

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
      setMessage("library-message", $("library-search").value.trim()
        ? t("No map has that in its name.")
        : t("This account has no maps yet. Make one in the Auto-Pigeon editor."));
      return;
    }
    for (const asset of assets) list.append(assetCard(asset));
    nextCursor = body.next_cursor || "";
    sayHowMany(Boolean(body.has_more && nextCursor));
  }

  // A person knows a map by the name they gave it. Ids, revision ids and cache
  // keys are the program's own bookkeeping and are never shown (operator,
  // NEW_244D). The ordinary action is the LATEST revision; an older one is
  // behind "Other revisions", for the rare time somebody needs it.
  function nameOf(asset) {
    return asset.display_name || "Untitled " + typeName(asset.asset_type).toLowerCase();
  }

  // A map's card: its name, when it was last saved, the textures it uses,
  // and one action. Compact, so a screen holds many (operator, 2026-09-22).
  function assetCard(asset) {
    const revision = asset.current_revision;
    const head = el("div", {
      className: "map-card__head",
      children: [
        el("h4", { text: nameOf(asset) }),
        asset.visibility ? badge(t(asset.visibility), asset.visibility === "public" ? "ok" : "") : null,
      ].filter(Boolean),
    });
    const saved = el("p", { className: "map-card__meta muted" });
    saved.textContent = revision
      ? t("Revision {n}", { n: revision.revision }) + (revision.created_at ? " · " + when(revision.created_at) : "")
      : t("Nothing saved yet");

    const textures = el("div", { className: "map-card__textures", attrs: { "aria-live": "polite" } });
    textures.append(el("span", { className: "muted small", text: t("Textures: checking…") }));
    if (revision) queueTextures(asset.asset_id, textures);
    else textures.replaceChildren();

    const status = el("p", { className: "message small", attrs: { role: "status" } });
    const play = el("button", { text: t("Build & Run"), attrs: { type: "button", class: "primary" } });
    play.disabled = !revision;
    // Build & Run opens on this map, at its newest revision.
    // The build and engine chosen before are kept; the map, its revision and
    // its name in the game start again.
    play.addEventListener("click", () => {
      const query = new URLSearchParams(window.location.search);
      for (const key of ["rev", "name", "step"]) query.delete(key);
      query.set("map", asset.asset_id);
      window.location.assign("?" + query.toString() + "#play");
    });
    const download = el("button", { text: t("Download"), attrs: { type: "button", class: "secondary" } });
    download.disabled = !revision;
    download.addEventListener("click", () =>
      withBusy(download, () => downloadRevision(asset, revision || {}, "current", status, download))
    );
    const other = el("button", { text: t("Older revisions…"), attrs: { type: "button", class: "link" } });
    other.addEventListener("click", () => openRevisions(asset, other));

    return el("li", {
      className: "card map-card",
      children: [head, saved, textures, el("div", { className: "map-card__actions", children: [play, download, other] }), status],
    });
  }

  // The textures each card shows, asked for a few at a time: a first page is
  // fifty maps, and fifty requests at once would be a burst for no reason.
  const textureQueue = [];
  let textureWorkers = 0;
  function queueTextures(mapID, node) {
    textureQueue.push({ mapID, node });
    while (textureWorkers < 4 && textureQueue.length) {
      textureWorkers += 1;
      (async () => {
        // A turn later, so the card this was queued for is on the page.
        await new Promise((resolve) => window.setTimeout(resolve, 0));
        while (textureQueue.length) {
          const job = textureQueue.shift();
          if (!document.contains(job.node)) continue;
          await drawTextures(job.mapID, job.node);
        }
        textureWorkers -= 1;
      })();
    }
  }

  async function drawTextures(mapID, node) {
    const { ok, body } = await api(`/api/v1/library/maps/${encodeURIComponent(mapID)}/textures`);
    node.replaceChildren();
    if (!ok) {
      node.append(el("span", { className: "muted small", text: t("Textures: could not be read") }));
      return;
    }
    const wads = body.wads_declared || [];
    node.append(el("span", { className: "map-card__label", text: t("Textures") }));
    if (!wads.length) {
      node.append(el("span", { className: "muted small", text: t("none declared") }));
      return;
    }
    const list = el("ul", { className: "wad-chips", attrs: { "aria-label": t("Texture WADs, in the order the map declares them") } });
    for (const wad of wads) {
      list.append(el("li", { className: "wad-chip", text: wad.replace(/^.*[\\/]/, ""), attrs: { title: wad } }));
    }
    node.append(list);
    if (body.texture_count) {
      node.append(el("span", { className: "muted small", text: t("{n} textures", { n: body.texture_count }) }));
    }
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
      setMessage(status, "Chosen. Build is open on it.", "ok");
      openBuildOnChosen();
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

  // "Use in a build" goes to the build: the Build area, at the map step, with
  // the revision already in the map field. It used to say "Chosen" and stay
  // here (NEW_244D).
  function openBuildOnChosen() {
    window.AUCOM.showArea("build");
    window.AUCOM.areas.build?.showStep?.(2);
  }

  async function refreshCached() {
    const list = $("cached-list");
    const { ok, body } = await api("/api/v1/library/cached");
    list.replaceChildren();
    if (!ok) {
      setMessage("cached-message", body.error || "could not read the local cache", "error");
      return;
    }
    // Maps only, like the list above: other kinds of download are the
    // editor's, and a build fetches what it needs itself.
    const items = (body.items || []).filter((item) => item.record?.asset_type === "map");
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
        setMessage("cached-message", "Chosen. Build is open on it.", "ok");
        openBuildOnChosen();
      });
      list.append(el("li", { children: [head, detail, el("div", { className: "row-actions", children: [use] })] }));
    }
  }

  $("library-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshCatalog));
  $("library-more").addEventListener("click", (event) => showMore(event.currentTarget));
  $("cached-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshCached));
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
        await refreshCatalog();
      } else {
        setMessage("library-message", "Sign in to see what is in your account.", "");
        $("library-assets").replaceChildren();
      }
    },
  };
})();
