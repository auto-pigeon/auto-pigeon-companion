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

  async function refreshTypes() {
    const select = $("library-type");
    if (select.options.length > 1) return;
    const { ok, body } = await api("/api/v1/library/capabilities");
    if (!ok) return;
    for (const type of body.asset_types || []) {
      select.append(el("option", { text: type.asset_type, attrs: { value: type.asset_type } }));
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

  function assetCard(asset) {
    const title = el("h4", { text: asset.display_name || asset.asset_id });
    const meta = el("p", { className: "muted" });
    meta.textContent = [asset.asset_type, asset.game, asset.visibility].filter(Boolean).join(" · ");

    const revision = asset.current_revision;
    const current = el("p", { className: "mono" });
    current.textContent = revision
      ? `current: revision ${revision.revision}${revision.revision_id ? " (" + revision.revision_id + ")" : ""}`
      : "no revisions yet";

    const open = el("button", {
      text: "Choose a revision",
      attrs: { type: "button" },
    });
    open.addEventListener("click", () => openRevisions(asset, open));

    return el("li", {
      className: "card",
      children: [title, meta, current, el("div", { className: "row-actions", children: [open] })],
    });
  }

  async function openRevisions(asset, trigger) {
    await withBusy(trigger, async () => {
      const panel = $("library-revisions-panel");
      const list = $("library-revisions");
      panel.hidden = false;
      $("library-revisions-title").textContent =
        "Revisions of " + (asset.display_name || asset.asset_id);
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

    const detail = el("p", { className: "mono" });
    detail.textContent = [
      key ? "id " + key : "no revision id (this type keeps a counter only)",
      revision.created_at ? "created " + when(revision.created_at) : "",
      revision.immutable ? "immutable" : "may change",
    ]
      .filter(Boolean)
      .join(" · ");

    const download = el("button", { text: held ? "Download again" : "Download", attrs: { type: "button" } });
    const use = el("button", { text: "Use in a build", attrs: { type: "button", class: "secondary" } });
    use.disabled = !held;

    const status = el("p", { className: "message", attrs: { role: "status" } });

    download.addEventListener("click", () =>
      withBusy(download, async () => {
        busy(status, "Downloading and verifying…");
        const { ok, body } = await api("/api/v1/library/sync", {
          method: "POST",
          body: {
            asset_type: asset.asset_type,
            asset_id: asset.asset_id,
            revision: key || "current",
          },
        });
        if (!ok) {
          setMessage(status, body.error || "the download failed", "error");
          record(`Download of ${asset.display_name || asset.asset_id} failed`, body.error, "failed");
          return;
        }
        const summary = body.already_complete
          ? "Already here — nothing was fetched."
          : `${body.fetched} file(s) fetched, ${body.reused} already held, ${bytes(body.bytes_fetched)} transferred.`;
        setMessage(status, summary, "ok");
        cachedKeys.add(body.key);
        use.disabled = false;
        download.textContent = "Download again";
        // The durable evidence: the cached list is re-read from disk, so what
        // the user is looking at afterwards is the server's record and not this
        // message.
        record(
          `Downloaded ${asset.display_name || asset.asset_id} revision ${body.record.revision}`,
          `${asset.asset_type}/${asset.asset_id} · ${body.key}`,
          "ok"
        );
        await refreshCached();
        chooseRevision(asset, body.record, body.key);
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
      display_name: asset.display_name || asset.asset_id,
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
        : `${items.length} revision${items.length === 1 ? "" : "s"} in ${body.root}.`
    );
    for (const item of items) {
      const record_ = item.record;
      const head = el("div", { className: "row-head" });
      head.append(el("strong", { text: record_.display_name || record_.asset_id }));
      head.append(badge(record_.asset_type, "ok"));
      const detail = el("p", { className: "mono" });
      detail.textContent = [
        `revision ${record_.revision}`,
        item.key,
        `${record_.files.length} file(s)`,
        bytes(record_.total_bytes),
        "synced " + when(record_.synced_at),
      ]
        .filter(Boolean)
        .join(" · ");

      const use = el("button", { text: "Use in a build", attrs: { type: "button", class: "secondary" } });
      use.addEventListener("click", () => {
        chooseRevision(
          { asset_type: record_.asset_type, asset_id: record_.asset_id, display_name: record_.display_name },
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
