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
    // Reading the account can take as long as its server takes to answer.
    // What is already on this computer needs no answer, and is said meanwhile.
    busy("library-message", cachedKeys.size
      ? t("Reading your account… Maps already downloaded are under On this computer.")
      : t("Reading your account…"));
    $("library-more").hidden = true;
    nextCursor = "";
    const query = catalogQuery("");

    const { ok, status, body } = await api("/api/v1/library/catalog?" + query.toString());
    list.replaceChildren();
    latestCards.clear();
    latestPending.clear();
    if (!ok) {
      // The exact cause, and the thing to do about it. A 401 here means the
      // session, not the password field the user is looking at.
      const hint =
        status === 401
          ? " Sign in again with the Sign in button at the top of the page."
          : status === 503
            ? " Choose your Auto-Pigeon server in Settings."
            : "";
      // The server did not answer at all (seen live, NEW_508A: a transport
      // error with the request's address was printed here). Said in words,
      // with what still works and the way to try again; the exact error stays
      // one click away.
      if (status !== 401 && status !== 403 && status !== 503 && status !== 404) {
        const retry = el("button", { text: t("Retry"), className: "secondary", attrs: { type: "button" } });
        retry.addEventListener("click", () => withBusy(retry, refreshCatalog));
        const message = $("library-message");
        message.className = "message error";
        message.replaceChildren(
          document.createTextNode(t("Your account's server did not answer, so your maps cannot be listed. Maps already downloaded are under On this computer and still build.") + " "),
          retry,
          el("details", { children: [el("summary", { text: t("Details") }), el("p", { className: "mono", text: body.error || "" })] }));
        return;
      }
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
    const download = el("button", { text: t("Download revision"), attrs: { type: "button", class: "secondary" } });
    download.disabled = !revision;
    download.addEventListener("click", () =>
      withBusy(download, () => downloadRevision(asset, revision || {}, "current", status, download))
    );
    // Older revisions are a dropdown on Download itself (operator, 2026-09-23):
    // the main half downloads the latest, the caret lists the older ones and
    // downloads the one chosen — AUP's split button (UI-DESIGN §3).
    const split = olderRevisions(asset, revision, download, status);

    // Play latest build (NEW_365): offered only when this machine has a
    // normal, verified build of this map — never for a map that only has leak
    // tests, which are diagnostics. build.Latest on the server decides which.
    const built = el("p", { className: "map-card__built muted small", attrs: { "aria-live": "polite" } });
    built.hidden = true;
    const playLatest = el("button", { text: t("Play latest build"), attrs: { type: "button", class: "secondary" } });
    playLatest.hidden = true;
    latestCards.set(asset.asset_id, { built, playLatest, current: revision });
    queueLatest(asset.asset_id);

    return el("li", {
      className: "card map-card",
      children: [head, saved, textures, built, status,
        el("div", { className: "map-card__actions row-actions", children: [split, playLatest, play] })],
    });
  }

  // --- the latest playable build of each card's map ---------------------------
  //
  // One question for every card drawn in the same moment, and only the latest
  // answer is applied: a page of maps read again (another account, another
  // server, a search) drops the cards the old answer was for, and an answer for
  // a card that is no longer on the page touches nothing (NEW_365 J7).
  const latestCards = new Map();
  let latestPending = new Set();
  let latestTimer = null;
  let latestSequence = 0;
  function queueLatest(assetID) {
    latestPending.add(assetID);
    clearTimeout(latestTimer);
    latestTimer = setTimeout(askLatest, 50);
  }
  async function askLatest() {
    const ids = [...latestPending].slice(0, 100);
    latestPending = new Set([...latestPending].slice(100));
    if (latestPending.size) latestTimer = setTimeout(askLatest, 50);
    if (!ids.length) return;
    const sequence = ++latestSequence;
    const asked = new Map(ids.map((id) => [id, latestCards.get(id)]));
    const query = new URLSearchParams();
    for (const id of ids) query.append("asset_id", id);
    const { ok, body } = await api("/api/v1/build/latest?" + query.toString());
    if (!ok) return;
    for (const selection of body.items || []) {
      const card = asked.get(selection.map?.asset_id);
      // A card drawn again since the question is not the card it was asked for.
      if (!card || latestCards.get(selection.map.asset_id) !== card || !card.built.isConnected) continue;
      showLatest(card, selection);
    }
    void sequence;
  }
  function showLatest(card, selection) {
    const { built, playLatest, current } = card;
    playLatest.hidden = selection.state !== "playable";
    built.hidden = false;
    if (selection.state === "playable") {
      const behind = current && selection.revision && selection.revision < current.revision;
      built.textContent = behind
        ? t("Built from revision {n}; newer changes are not included.", { n: selection.revision })
        : t("Latest build: revision {n}.", { n: selection.revision });
      playLatest.onclick = async () => {
        window.AUCOM.showArea("run");
        await window.AUCOM.areas.run?.chooseBuild?.(selection.build_id, {
          revision: selection.revision, family: selection.family, mapID: selection.map.asset_id,
          current: current?.revision, behind: Boolean(behind),
        });
      };
    } else if (selection.state === "unavailable") {
      built.textContent = t("The latest build's level is missing or was changed. Build it again to play it.");
    } else if (selection.leak_tests > 0) {
      built.textContent = t("No playable build yet: only leak tests.");
    } else {
      built.hidden = true;
    }
  }

  // The textures each card shows, asked for a few at a time: a first page is
  // fifty maps, and fifty requests at once would be a burst for no reason.
  const textureQueue = [];
  let textureWorkers = 0;
  function queueTextures(mapID, node, summary) {
    if (summary) {
      // Read from this computer already: nothing to ask, connected or not.
      drawTextures(mapID, node, summary);
      return;
    }
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

  // What one source's state means, for its chip's title and the line under
  // the chips (#6). The server decides the state; this only words it.
  const packageWords = {
    verified: "on this computer, verified for this revision",
    available: "in your account; a build fetches it",
    bound: "bound to an exact revision in your account",
    own_copy_needed: "not sent by the deployment: a build needs your own copy",
    from_game: "installed with the game; read from the game folder bound in Profiles",
    unavailable: "unavailable",
    missing: "missing: nothing supplies it",
  };

  function packageTitle(item) {
    const parts = [item.name];
    if (item.revision) parts.push(t("revision {n}", { n: item.revision }));
    parts.push(t(packageWords[item.state] || item.state));
    if (item.detail) parts.push(item.detail);
    return parts.join(" · ");
  }

  // summaryLine is the one sentence over every source, or "" when the chips
  // say it all.
  function summaryLine(summary) {
    const named = (state) => (summary.packages || []).filter((item) => item.state === state).map((item) => item.name).join(", ");
    switch (summary.state) {
      case "verified": return t("On this computer, verified.");
      case "needs_own_files": return t("Needs your own copy of {names}.", { names: named("own_copy_needed") });
      case "unavailable": {
        const names = [named("unavailable"), named("missing")].filter(Boolean).join(", ");
        return names ? t("Unavailable: {names}.", { names }) : t("Declared, and nothing could be resolved.");
      }
      case "game_folders": return t("From the game folders bound in Profiles.");
      default: return "";
    }
  }

  // drawTextures fills one card's textures row. summary is given for a card
  // of this computer (read with no network); otherwise the account is asked.
  async function drawTextures(mapID, node, given) {
    let body = { summary: given };
    if (!given) {
      const answer = await api(`/api/v1/library/maps/${encodeURIComponent(mapID)}/textures`);
      if (!answer.ok) {
        node.replaceChildren(el("span", { className: "muted small", text: t("Textures: could not be read") }));
        return;
      }
      body = answer.body;
    }
    node.replaceChildren();
    // The sentence over every source sits on its own line under the row: the
    // row itself is one line that never grows the card (operator, 2026-09-23).
    let stateLine = node.nextElementSibling?.classList.contains("map-card__texture-state") ? node.nextElementSibling : null;
    if (stateLine) stateLine.remove();
    const showState = (text, ok, refusals) => {
      if (!text) return;
      stateLine = el("p", { className: "small map-card__texture-state" + (ok ? "" : " muted"), text });
      if ((refusals || []).length) stateLine.title = refusals.join("; ");
      node.after(stateLine);
    };
    const summary = body.summary || { state: (body.wads_declared || []).length ? "available" : "none_declared",
      packages: (body.wads_declared || []).map((name) => ({ name, state: "available" })) };
    const packages = summary.packages || [];
    node.dataset.textures = summary.state;
    node.append(el("span", {
      className: "map-card__label",
      text: body.texture_count ? t("Textures ({n})", { n: body.texture_count }) : t("Textures"),
    }));
    if (!packages.length) {
      const line = summary.state === "none_declared" ? t("none declared") : summaryLine(summary);
      node.append(el("span", { className: "muted small", text: line || t("none declared") }));
      return;
    }
    showState(summaryLine(summary), summary.state === "verified", summary.refusals);
    const wads = packages.map((item) => item.name);
    const list = el("ul", { className: "wad-chips", attrs: { "aria-label": t("Texture sources, in the order the map declares them") } });
    for (const item of packages) {
      list.append(el("li", {
        className: "wad-chip wad-chip--" + item.state,
        text: item.name.replace(/^.*[\\/]/, "") + (item.revision ? " r" + item.revision : ""),
        attrs: { title: packageTitle(item), "data-state": item.state },
      }));
    }
    node.append(list);
    // The chips never grow the card (operator, 2026-09-23): one line, and when
    // they do not fit, a "…" that lists them all as a dropdown.
    window.requestAnimationFrame(() => {
      if (list.scrollWidth <= list.clientWidth + 1) return;
      const more = el("button", {
        text: "…",
        attrs: { type: "button", class: "secondary wad-more", "aria-haspopup": "menu", "aria-expanded": "false",
          "aria-label": t("All {n} texture WADs", { n: wads.length }), title: t("All {n} texture WADs", { n: wads.length }) },
      });
      const menu = el("div", { className: "menu wad-menu", attrs: { role: "menu" } });
      menu.hidden = true;
      menu.append(el("ul", { className: "wad-menu__list", children: wads.map((wad) => el("li", { text: wad.replace(/^.*[\\/]/, ""), attrs: { title: wad } })) }));
      const close = () => { menu.hidden = true; more.setAttribute("aria-expanded", "false"); };
      more.addEventListener("click", () => {
        if (!menu.hidden) return close();
        menu.hidden = false;
        more.setAttribute("aria-expanded", "true");
      });
      document.addEventListener("pointerdown", (event) => {
        if (!menu.hidden && !menu.contains(event.target) && !more.contains(event.target)) close();
      });
      menu.addEventListener("keydown", (event) => { if (event.key === "Escape") { close(); more.focus(); } });
      node.append(el("div", { className: "wad-more__wrap", children: [more, menu] }));
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
    // "Ready to build" is said only when it is true with no connection: the
    // map AND the texture bundle of this exact revision are on this computer
    // (issue #38 — the download used to stop at the map, and a build offline
    // then failed at "Downloading the textures").
    const got = body.already_complete ? t("Already on this computer.") : t("Downloaded ({size}).", { size: bytes(body.bytes_fetched) });
    const textures = body.textures;
    if (!textures || textures.state === "cached") {
      setMessage(status, got + " " + t("It is ready to build, with or without a connection."), "ok");
    } else if (textures.state === "needs_own_files") {
      // Buildable, with the person's own copy of what the deployment does
      // not send (#9): Build takes it from the Textures folder they choose.
      setMessage(status, got + " " + t("Its textures are here except files the deployment does not send; a build asks for a folder with your own copy of them."), "");
    } else if (textures.state === "incomplete") {
      // Here, and still not buildable: the revision's own textures are
      // incomplete, which a build refuses connected or not.
      status.className = "message small";
      status.replaceChildren(
        document.createTextNode(got + " " + t("This revision's textures are incomplete, so a build of it is refused until the map is saved with them. Build & Run says which.") + " "),
        el("details", { children: [el("summary", { text: t("Details") }), el("p", { className: "mono", text: (textures.refusals || []).join("; ") })] }));
    } else {
      status.className = "message small error";
      status.replaceChildren(
        document.createTextNode(got + " " + t("Its textures could not be downloaded, so building it still needs a connection.") + " "),
        el("details", { children: [el("summary", { text: t("Details") }), el("p", { className: "mono", text: textures.error || "" })] }));
    }
    cachedKeys.add(body.key);
    if (button) button.textContent = t("Download again");
    record(`Downloaded ${nameOf(asset)}, revision ${body.record.revision}`, "", "ok");
    await refreshCached();
    chooseRevision(asset, body.record, body.key);
    return true;
  }

  // olderRevisions is Download with its caret: the button, and a menu of the
  // map's earlier revisions read from the account the first time it opens.
  function olderRevisions(asset, current, download, status) {
    const caret = el("button", {
      text: "▾",
      attrs: { type: "button", class: "secondary btn-split__caret", "aria-haspopup": "menu", "aria-expanded": "false",
        "aria-label": t("Older revisions"), title: t("Older revisions") },
    });
    caret.disabled = !current;
    const menu = el("div", { className: "menu btn-split__menu", attrs: { role: "menu" } });
    menu.hidden = true;
    let loaded = false;
    const close = () => {
      menu.hidden = true;
      caret.setAttribute("aria-expanded", "false");
    };
    caret.addEventListener("click", async () => {
      if (!menu.hidden) return close();
      menu.hidden = false;
      caret.setAttribute("aria-expanded", "true");
      if (loaded) return;
      menu.replaceChildren(el("p", { className: "menu-note", text: t("Reading…") }));
      const { ok, body } = await api(
        `/api/v1/library/assets/${encodeURIComponent(asset.asset_type)}/${encodeURIComponent(asset.asset_id)}`
      );
      menu.replaceChildren();
      if (!ok) {
        menu.append(el("p", { className: "menu-note", text: body.error || t("The revisions could not be read.") }));
        return;
      }
      loaded = true;
      const older = (body.revisions || []).filter((revision) => revision.revision !== current?.revision);
      if (older.length === 0) {
        menu.append(el("p", { className: "menu-note", text: t("There are no older revisions.") }));
        return;
      }
      for (const revision of older) {
        const item = el("button", {
          text: [t("Revision {n}", { n: revision.revision }), revision.created_at ? when(revision.created_at) : ""].filter(Boolean).join(" · "),
          attrs: { type: "button", role: "menuitem" },
        });
        item.addEventListener("click", async () => {
          close();
          await withBusy(download, () => downloadRevision(asset, revision, revision.revision_id || "", status, null));
        });
        menu.append(item);
      }
    });
    document.addEventListener("pointerdown", (event) => {
      if (!menu.hidden && !menu.contains(event.target) && !caret.contains(event.target)) close();
    });
    menu.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        close();
        caret.focus();
      }
    });
    return el("div", { className: "btn-split", children: [download, caret, menu] });
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
    for (const item of items) cachedKeys.add(item.key);
    setMessage(
      "cached-message",
      items.length === 0
        ? "Nothing downloaded yet."
        : t("{n} downloaded to this computer.", { n: items.length })
    );
    // A card like the ones in Your maps: name, revision and size, the WADs it
    // uses, and its actions bottom right (operator, 2026-09-23).
    for (const item of items) {
      const record_ = item.record;
      // A record made before the name was known still gets one from the
      // account's own listing, when this page has read it.
      const known = assets.find((asset) => asset.asset_id === record_.asset_id);
      const name = record_.display_name || known?.display_name || "Untitled " + typeName(record_.asset_type).toLowerCase();
      const head = el("div", { className: "map-card__head", children: [el("h4", { text: name }), badge(t("downloaded"), "ok")] });
      const meta = el("p", {
        className: "map-card__meta muted",
        text: [t("Revision {n}", { n: record_.revision }), bytes(record_.total_bytes), when(record_.synced_at)].filter(Boolean).join(" · "),
      });
      const textures = el("div", { className: "map-card__textures" });
      // This revision's own summary, from this computer's verified cache or
      // its Quake III ledger (#6); the account's latest only when neither.
      queueTextures(record_.asset_id, textures, item.texture_summary);
      const status = el("p", { className: "message small", attrs: { role: "status" } });
      // Whether THIS revision builds with no connection, from this computer's
      // own cache and no network (#38): the map is here; its texture bundle
      // either is too, or a build will have to fetch it.
      if (item.textures === "cached") setMessage(status, t("Map and textures are on this computer: it builds without a connection."), "ok");
      else if (item.textures === "needs_own_files") setMessage(status, t("Map and textures are on this computer, except files the deployment does not send: a build needs your own copy of them, from a folder you choose."), "");
      else if (item.textures === "incomplete") setMessage(status, t("Map and texture information are on this computer, but this revision's textures are incomplete: a build of it is refused."), "");
      else if (item.textures === "missing") setMessage(status, t("The map is on this computer; its textures are not, so building it needs a connection. Download it again while connected."), "");

      const use = el("button", { text: t("Use in a build"), attrs: { type: "button", class: "secondary" } });
      use.addEventListener("click", () => {
        chooseRevision({ asset_type: record_.asset_type, asset_id: record_.asset_id, display_name: name }, record_, item.key);
        setMessage(status, "Chosen. Build is open on it.", "ok");
        openBuildOnChosen();
      });
      const play = el("button", { text: t("Build & Run"), attrs: { type: "button", class: "primary" } });
      play.addEventListener("click", () => {
        const query = new URLSearchParams(window.location.search);
        for (const key of ["rev", "name", "step"]) query.delete(key);
        query.set("map", record_.asset_id);
        if (record_.revision_id) query.set("rev", record_.revision_id);
        window.location.assign("?" + query.toString() + "#play");
      });
      list.append(el("li", {
        className: "card map-card",
        children: [head, meta, textures, status, el("div", { className: "map-card__actions row-actions", children: [use, play] })],
      }));
    }
  }

  // --- a map file of one's own (NEW_265) -------------------------------------
  //
  // Not an account revision, and never presented as one: the file is chosen
  // with this machine's own file chooser (or typed, where there is none),
  // checked like any typed path, and handed to the ordinary Build wizard at
  // its Map step. The pipeline, the WAD folder and the preview are all the
  // wizard's, as they are for any file on this machine — there is no second
  // way to build. Nothing is uploaded, and the file itself is only ever read.

  // The map extensions the installed pipelines' map inputs accept, from their
  // own declarations: a `.map` for the Quake pipelines, and whatever else a
  // pipeline on this machine truly takes.
  async function acceptedMapExtensions() {
    const { ok, body } = await api("/api/v1/build/pipelines");
    const extensions = new Set();
    if (ok) {
      for (const pipeline of body.items || []) {
        for (const input of pipeline.inputs || []) {
          if (input.source_kind !== "map") continue;
          for (const extension of input.extensions || []) extensions.add(extension.toLowerCase());
        }
      }
    }
    return [...extensions].sort();
  }

  async function useLocalMap(path) {
    const message = $("local-map-message");
    const accepted = await acceptedMapExtensions();
    const checked = await api("/api/v1/paths/validate", { method: "POST", body: { kind: "open-file", path } });
    if (!checked.body.valid) {
      setMessage(message, checked.body.error || t("That file cannot be used."), "error");
      return false;
    }
    const resolved = checked.body.path;
    const lower = resolved.toLowerCase();
    if (accepted.length && !accepted.some((extension) => lower.endsWith(extension))) {
      setMessage(message, t("{name} is not a map file a build profile here accepts. Choose a {kinds} file.", {
        name: resolved.split(/[\\/]/).pop(), kinds: accepted.join(" or "),
      }), "error");
      return false;
    }
    setMessage(message, t("Opening Build on {name}…", { name: resolved.split(/[\\/]/).pop() }), "");
    record(`Chose a map file on this computer`, resolved, "ok");
    await window.AUCOM.areas.build?.useLocalFile?.(resolved);
    return true;
  }

  function showTypedPath() {
    const holder = $("local-map-typed");
    if (!holder.hidden) return;
    const field = window.AUCOM.pathField({
      id: "local-map-path", kind: "open-file", label: t("The map file"),
      hint: t("This machine has no file chooser the Companion can open, so type the file's full path."),
    });
    const use = el("button", { text: t("Build this map"), attrs: { type: "button", class: "primary" } });
    use.addEventListener("click", () => withBusy(use, async () => {
      const path = field.input.value.trim();
      if (!path) {
        setMessage("local-map-message", t("Type the map file's path first."), "error");
        return;
      }
      await useLocalMap(path);
    }));
    holder.replaceChildren(field.container, el("div", { className: "row-actions", children: [use] }));
    holder.hidden = false;
    field.input.focus();
  }

  $("local-map-choose").addEventListener("click", (event) => withBusy(event.currentTarget, async () => {
    const message = $("local-map-message");
    const accepted = await acceptedMapExtensions();
    setMessage(message, t("A file chooser is open. If you can't see it, look on the taskbar."));
    const { ok, status, body } = await api("/api/v1/paths/pick", {
      method: "POST",
      body: {
        kind: "open-file", title: t("Choose a map file"),
        filters: accepted.length ? [{ name: t("Map files"), extensions: accepted.map((e) => e.replace(/^\./, "")) }] : undefined,
      },
    });
    if (ok && body.cancelled) {
      // Somebody was asked and said no. Nothing is wrong, nothing changes.
      setMessage(message, t("No file was chosen."), "");
      return;
    }
    if (!ok) {
      if (status === 501) {
        showTypedPath();
        return;
      }
      setMessage(message, (body.error || t("The file chooser could not be opened.")) + " " + t("Type the path instead."), "error");
      showTypedPath();
      return;
    }
    setMessage(message, "");
    await useLocalMap(body.path);
  }));

  $("library-view").addEventListener("change", () => {
    const local = $("library-view").value === "local";
    $("library-account-panel").hidden = local;
    $("library-local-panel").hidden = !local;
  });

  $("library-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshCatalog));
  $("library-more").addEventListener("click", (event) => showMore(event.currentTarget));
  $("cached-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshCached));
  $("library-search").addEventListener("change", refreshCatalog);

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
