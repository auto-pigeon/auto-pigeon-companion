// The Games area — AUB/AUG/AUCOM/AUT 244F.
//
// Everything this area says about whether somebody can join comes from one
// readiness report the server computes (internal/joinready). The page renders
// it and offers the ONE next action the report names; it never decides "ready"
// itself. Normal copy names people's things — the engine, the game folder, the
// map files — and never an id or a digest; the Details box carries provenance
// for whoever wants it.
//
// A clicked autopigeon:// link arrives here as a pending game: the server
// redeems it once, and this area opens that game's readiness. Nothing starts
// until the exact command has been shown and "Start the game" is pressed.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, announce, record, badge, bytes, t } = window.AUCOM;

  let cursor = "";
  let current = null; // { id, readiness }

  const familyNames = { quake1: "Quake", quake2: "Quake II", quake3: "Quake III" };

  function signedIn() {
    return Boolean(window.AUCOM.status?.authenticated);
  }

  function showSignedOut(visible) {
    $("games-signed-out").hidden = !visible;
    $("games-list-panel").hidden = visible;
    if (visible) $("games-detail").hidden = true;
  }

  // --- the listing -----------------------------------------------------------

  async function loadList({ more = false } = {}) {
    if (!signedIn()) {
      showSignedOut(true);
      return;
    }
    showSignedOut(false);
    if (!more) cursor = "";
    const query = new URLSearchParams();
    query.set("scope", $("games-scope").value);
    if ($("games-family").value) query.set("game_family", $("games-family").value);
    if ($("games-mode").value) query.set("mode", $("games-mode").value);
    if (more && cursor) query.set("cursor", cursor);
    busy("games-message", "Asking Auto-Pigeon who is hosting…");
    const { ok, status, body } = await api("/api/v1/games?" + query.toString());
    if (!ok) {
      if (status === 401) {
        showSignedOut(true);
        setMessage("games-message", "");
        return;
      }
      setMessage("games-message", body.error || "The games could not be listed.", "error");
      return;
    }
    const games = body.games || [];
    cursor = body.next_cursor || "";
    $("games-more").hidden = !cursor;
    if (!more) $("games-list").replaceChildren();
    for (const game of games) $("games-list").append(gameRow(game));
    setMessage(
      "games-message",
      $("games-list").children.length === 0
        ? $("games-scope").value === "mine"
          ? "You are not hosting anything, and have not recently."
          : "Nobody is hosting a public game right now."
        : ""
    );
  }

  // One game, one small square card (operator, 2026-09-23: a long line was
  // mostly empty space): the title, who hosts it, the facts, and Join bottom
  // right. The title opens the game's own page.
  function gameRow(game) {
    const live = game.state === "live";
    const title = el("button", { className: "link game-title", text: game.title, attrs: { type: "button" } });
    title.addEventListener("click", () => openGame(game.id));
    const head = el("div", { className: "row-head", children: [title, badge(live ? "live" : "ended", live ? "running" : "cancelled")] });
    const lines = [
      game.host_nickname ? `Hosted by ${game.host_nickname}` : "",
      [game.map_name, engineName(game)].filter(Boolean).join(" · "),
      [playersShort(game), game.join_content?.state === "required" ? bytes(game.join_content.total_bytes) : "", freshness(game.stale_for_ms)]
        .filter(Boolean).join(" · "),
    ].filter(Boolean).map((text) => el("p", { className: "muted game-fact", text }));
    const join = el("button", { className: "primary", text: "Join", attrs: { type: "button" } });
    join.disabled = !(live && game.joinable);
    if (join.disabled) join.title = live ? "This game cannot be joined from this account." : "This game has ended.";
    join.addEventListener("click", async () => {
      await openGame(game.id, { quiet: true });
      if (current?.id === game.id) await joinNow($("games-primary"));
    });
    return el("li", {
      className: "card game-card",
      children: [head, ...lines, el("div", { className: "row-actions", children: [join] })],
    });
  }

  function engineName(game) {
    const runtime = String(game.engine_runtime || "");
    const known = { vkquake: "vkQuake", quakespasm: "QuakeSpasm", ironwail: "Ironwail", darkplaces: "DarkPlaces", fteqw: "FTEQW" };
    const name = known[runtime.toLowerCase()] || runtime;
    return [name, game.engine_version].filter(Boolean).join(" ");
  }

  function playersShort(game) {
    if (!game.players_observable) return "";
    return `${game.players_current}/${game.players_max} players`;
  }

  function freshness(ms) {
    const seconds = Math.round((ms || 0) / 1000);
    if (seconds < 5) return "just now";
    if (seconds < 120) return `${seconds} seconds ago`;
    return `${Math.round(seconds / 60)} minutes ago`;
  }

  // --- one game ----------------------------------------------------------------

  async function openGame(id, { quiet = false } = {}) {
    if (!id) return;
    started.delete(id); // re-read: the engine may have been closed since
    if (window.location.hash !== "#games/" + id) {
      window.history.replaceState(null, "", "#games/" + id);
    }
    $("games-detail").hidden = false;
    $("games-action").hidden = true;
    $("games-action").replaceChildren();
    if (!quiet) busy("games-detail-message", "Checking what this computer needs…");
    const { ok, status, body } = await api("/api/v1/games/" + encodeURIComponent(id));
    if (!ok) {
      if (status === 401) {
        showSignedOut(true);
        return;
      }
      if (body.readiness) render(id, body.readiness);
      setMessage("games-detail-message", body.error || "This game could not be read.", "error");
      return;
    }
    render(id, body.readiness);
    setMessage("games-detail-message", "");
    $("games-detail").scrollIntoView({ block: "start" });
  }

  function render(id, readiness) {
    current = { id, readiness };
    $("games-detail-title").textContent = readiness.title || "Game";

    // The facts on one line, each once: the engine is named here and not
    // again by the checklist below.
    const address = readiness.endpoint
      ? `${readiness.endpoint} (${reachability(readiness.details?.reachability)})`
      : "address not shown to your account";
    $("games-facts").replaceChildren(el("p", {
      className: "muted",
      text: [
        readiness.host ? `Hosted by ${readiness.host}` : "",
        readiness.map_name,
        readiness.engine?.name,
        readiness.players,
        address,
        readiness.package_bytes ? `${bytes(readiness.package_bytes)} of map files` : "",
        readiness.freshness ? `heard ${readiness.freshness}` : "",
      ].filter(Boolean).join(" · "),
    }));

    // What is already fine folds into one line; only what is left is listed.
    const steps = $("games-steps");
    steps.replaceChildren();
    const done = (readiness.steps || []).filter((step) => step.done);
    if (done.length) {
      steps.append(el("li", { className: "join-step done", children: [
        el("p", { className: "muted", text: "Ready: " + done.map((step) => step.title).filter((title, i, all) => all.indexOf(title) === i).join(" · ") }),
      ] }));
    }
    for (const step of (readiness.steps || []).filter((step) => !step.done)) {
      const next = step.id === readiness.next;
      const item = el("li", { className: "join-step" + (next ? " next" : "") });
      const head = el("div", { className: "row-head" });
      head.append(el("strong", { text: step.title }));
      head.append(badge(next ? "next" : "waiting", next ? "warning" : "cancelled"));
      item.append(head);
      if (step.detail) item.append(el("p", { className: "muted", text: step.detail }));
      if (step.warning) item.append(el("p", { className: "join-warning", text: step.warning }));
      steps.append(item);
    }

    const provenance = $("games-provenance");
    provenance.replaceChildren();
    const details = readiness.details || {};
    const add = (term, value) => {
      if (value === undefined || value === null || value === "") return;
      provenance.append(el("dt", { text: term }), el("dd", { className: "mono", text: String(value) }));
    };
    add("Game", details.game_id);
    add("Map revision", details.map_revision);
    add("Game family", details.game_family);
    add("Host's engine", details.engine_runtime);
    add("Host's engine profile", details.host_engine_profile);
    add("Map files package", details.package);
    add("Content the host named", details.content_identity);
    add("Player counts from", details.players_source === "host" ? "the host's own server" : details.players_source);
    add("Reachability", details.reachability);

    const primary = $("games-primary");
    primary.hidden = false;
    primary.disabled = false;
    primary.title = "";
    if (running(id)) {
      // Pressing it again would only find the engine already running.
      primary.textContent = "Running";
      primary.disabled = true;
      primary.title = "This game is running on this computer; see Jobs to follow or stop it.";
      return;
    }
    switch (readiness.state) {
      case "ready_for_review":
        primary.textContent = "Join";
        break;
      case "setup_required":
        // Downloading the map files is part of JOINING, not of setting this
        // computer up: when that is all that is left, the button says Join.
        primary.textContent = nextStep()?.action === "download_join_content" ? "Join" : "Set up to join";
        break;
      case "sign_in_required":
        primary.textContent = "Sign in";
        break;
      default:
        primary.textContent = "Join";
        primary.disabled = true;
        primary.title = readiness.summary || "This game cannot be joined from this computer now.";
    }
  }

  // The games this window started, so the button says so rather than offering
  // to start a second engine.
  const started = new Set();
  function running(id) {
    return started.has(id);
  }

  function reachability(word) {
    return (
      {
        local: "on the host's own network",
        unverified: "nobody has checked it from outside",
        verified: "answered when last checked",
        unreachable: "did not answer when last checked",
      }[word] || word || ""
    );
  }

  function nextStep() {
    const readiness = current?.readiness;
    return (readiness?.steps || []).find((step) => step.id === readiness.next);
  }

  // --- the one next action ------------------------------------------------------

  async function primaryAction(button) {
    if (!current) return;
    const readiness = current.readiness;
    if (readiness.state === "sign_in_required") {
      window.AUCOM.openSignIn(button);
      return;
    }
    if (readiness.state === "ready_for_review" || nextStep()?.action === "download_join_content") {
      await joinNow(button);
      return;
    }
    const step = nextStep();
    switch (step?.action) {
      case "install_engine_profile":
        return installProfile(button);
      case "approve_engine_profile":
        return approveProfile(button);
      case "choose_engine_executable":
        return chooseProgram(button);
      case "choose_game_folder":
        return chooseFolder(button);
      case "download_join_content":
        return downloadContent(button);
      case "sign_in":
        window.AUCOM.openSignIn(button);
        return;
      default:
        setMessage("games-detail-message", step ? step.detail : "There is nothing this computer can do next.", "error");
    }
  }

  function actionPanel(children) {
    const panel = $("games-action");
    panel.replaceChildren(...children);
    panel.hidden = false;
    return panel;
  }

  async function afterStep(response, success) {
    const { ok, body } = response;
    if (!ok) {
      if (body.readiness) render(current.id, body.readiness);
      setMessage("games-detail-message", body.error || "That did not work.", "error");
      return false;
    }
    $("games-action").hidden = true;
    record(success, current.readiness.title, "ok");
    announce(success);
    // Back to the same game, re-checked from scratch.
    await openGame(current.id, { quiet: true });
    setMessage("games-detail-message", success, "ok");
    return true;
  }

  async function installProfile(button) {
    await withBusy(button, async () => {
      busy("games-detail-message", "Reading the host's engine profile from Auto-Pigeon…");
      const { ok, body } = await api(`/api/v1/games/${encodeURIComponent(current.id)}/engine-profile/review`, { method: "POST", body: {} });
      if (!ok) {
        setMessage("games-detail-message", body.error || "The profile could not be read.", "error");
        return;
      }
      setMessage("games-detail-message", "");
      showProfileReview(body.review);
    });
  }

  function showProfileReview(review) {
    const install = el("button", { className: "primary", text: "Install and approve", attrs: { type: "button" } });
    const cancel = el("button", { className: "secondary", text: "Cancel", attrs: { type: "button" } });
    const lines = [
      el("h4", { text: `${review.name} ${review.version}` }),
      el("p", {
        text:
          "This profile describes how to start the engine. It was published on Auto-Pigeon" +
          (review.deployment_trust ? ` (marked “${review.deployment_trust}” there)` : "") +
          ", and on this computer it will be a community profile you approved yourself. It contains no program and no game data.",
      }),
    ];
    if (review.yanked) lines.push(el("p", { className: "join-warning", text: `Its publisher withdrew this version: ${review.yank_reason || "no reason given"}.` }));
    lines.push(el("div", { className: "row-actions", children: [install, cancel] }));
    actionPanel(lines);
    cancel.addEventListener("click", () => ($("games-action").hidden = true));
    install.addEventListener("click", () =>
      withBusy(install, async () => {
        busy("games-detail-message", "Installing and approving…");
        const response = await api(`/api/v1/games/${encodeURIComponent(current.id)}/engine-profile/install`, {
          method: "POST",
          body: { digest: review.digest, approve: true },
        });
        if (!response.ok && response.body.code === "publication_changed") {
          setMessage("games-detail-message", response.body.error, "error");
          showProfileReview(response.body.review);
          return;
        }
        await afterStep(response, `${review.name} is installed and approved.`);
      })
    );
    install.focus();
  }

  async function approveProfile(button) {
    await withBusy(button, async () => {
      const { ok, body } = await api(`/api/v1/games/${encodeURIComponent(current.id)}/engine-profile/permissions`);
      if (!ok) {
        setMessage("games-detail-message", body.error || "The profile could not be read.", "error");
        return;
      }
      const approve = el("button", { className: "primary", text: "Approve", attrs: { type: "button" } });
      const cancel = el("button", { className: "secondary", text: "Cancel", attrs: { type: "button" } });
      actionPanel([
        el("p", { text: `Approve ${body.name} on this computer? It describes how to start the engine you already have.` }),
        el("div", { className: "row-actions", children: [approve, cancel] }),
      ]);
      cancel.addEventListener("click", () => ($("games-action").hidden = true));
      approve.addEventListener("click", () =>
        withBusy(approve, async () => {
          const response = await api(`/api/v1/games/${encodeURIComponent(current.id)}/engine-profile/approve`, {
            method: "POST",
            body: { digest: body.digest },
          });
          await afterStep(response, `${body.name} is approved on this computer.`);
        })
      );
      approve.focus();
    });
  }

  async function bind(body) {
    const engine = current.readiness.engine;
    return api(`/api/v1/profiles/${encodeURIComponent(engine.profile_id)}/bind`, { method: "POST", body });
  }

  // pick opens the desktop's own chooser. Where there is none, a text field is
  // offered instead — the same fallback every other path in the Companion has.
  async function pick(kind, title, save) {
    const { ok, status, body } = await api("/api/v1/paths/pick", { method: "POST", body: { kind, title } });
    if (ok && body.cancelled) return;
    if (ok) {
      await save(body.path);
      return;
    }
    if (status !== 501) {
      setMessage("games-detail-message", body.error || "The chooser could not be opened.", "error");
      return;
    }
    const field = window.AUCOM.pathField({ id: "games-path", label: title, kind });
    const use = el("button", { className: "primary", text: "Use this", attrs: { type: "button" } });
    actionPanel([field.container, el("div", { className: "row-actions", children: [use] })]);
    use.addEventListener("click", () =>
      withBusy(use, async () => {
        const path = await field.check();
        if (path) await save(path);
      })
    );
    field.input.focus();
  }

  async function chooseProgram(button) {
    const engine = current.readiness.engine;
    await withBusy(button, () =>
      pick("open-file", `Choose the ${engine.executable || engine.name} program`, async (path) => {
        const executables = {};
        executables[engine.executable_name || "engine"] = path;
        await afterStep(await bind({ executables }), `${engine.name} will start from the program you chose.`);
      })
    );
  }

  async function chooseFolder(button) {
    const engine = current.readiness.engine;
    const inside = (engine.base_dirs || ["id1"]).join(" or ");
    await withBusy(button, () =>
      pick("directory", `Choose the folder that contains ${inside}`, async (path) => {
        await afterStep(await bind({ roots: { game_root: path } }), "Your game folder is set. It is read where it is.");
      })
    );
  }

  async function downloadContent(button) {
    let downloaded = false;
    await withBusy(button, async () => {
      busy("games-detail-message", "Downloading this game's map files and checking every one…");
      const response = await api(`/api/v1/games/${encodeURIComponent(current.id)}/content`, { method: "POST", body: {} });
      downloaded = await afterStep(response, "The map files are downloaded, checked and ready.");
    });
    return downloaded;
  }

  // --- joining, in one press ------------------------------------------------------
  //
  // Operator, 2026-09-23: "joining should be an atomic operation: the user
  // presses join, all the data is downloaded and the game opens immediately
  // after". Join downloads and checks the map files if they are not here yet,
  // spends a fresh link, re-checks that the game has not moved, and starts the
  // engine — the press IS the approval. What ran stays on the page and in Jobs.
  // Only a choice that is the person's to make — which engine program, which
  // game folder — stops it, and then Join opens that step instead.

  async function joinNow(button) {
    for (let guard = 0; guard < 3 && current; guard++) {
      const readiness = current.readiness;
      if (readiness.state === "ready_for_review") {
        await reviewAndLaunch(button);
        return;
      }
      if (nextStep()?.action !== "download_join_content") {
        await primaryAction(button);
        return;
      }
      if (!(await downloadContent(button))) return;
    }
  }

  async function reviewAndLaunch(button) {
    await withBusy(button, async () => {
      busy("games-detail-message", "Joining: checking the game has not changed and starting the engine…");
      const review = await api(`/api/v1/games/${encodeURIComponent(current.id)}/review`, { method: "POST", body: {} });
      if (!review.ok) {
        if (review.body.readiness) render(current.id, review.body.readiness);
        setMessage("games-detail-message", review.body.error || "The join could not be prepared.", "error");
        return;
      }
      const plan = review.body;
      const { ok, body } = await api(`/api/v1/games/${encodeURIComponent(current.id)}/launch`, {
        method: "POST",
        body: { plan_id: plan.plan_id, approve: true },
      });
      if (!ok) {
        setMessage("games-detail-message", body.error || "The game could not be started.", "error");
        return;
      }
      const message = body.already
        ? "This game is already running on this computer; see Jobs."
        : `${plan.engine} is starting and connecting to ${plan.endpoint}.`;
      const ran = el("details", {
        children: [
          el("summary", { text: "What ran" }),
          el("pre", { className: "output", text: plan.preview?.shell || "" }),
          el("p", { className: "muted", text: `in ${plan.preview?.working_dir || ""}` }),
        ],
      });
      const children = [el("p", { text: message })];
      for (const warning of plan.warnings || []) children.push(el("p", { className: "join-warning", text: warning }));
      const jobs = el("button", { className: "secondary", text: "Open Jobs", attrs: { type: "button" } });
      jobs.addEventListener("click", () => window.AUCOM.showArea("jobs"));
      children.push(ran, el("div", { className: "row-actions", children: [jobs] }));
      actionPanel(children);
      setMessage("games-detail-message", message, "ok");
      record(body.already ? "Join already running" : "Joined a game", current.readiness.title, "ok");
      started.add(current.id);
      render(current.id, current.readiness);
    });
  }

  // --- a link the operating system delivered ------------------------------------------

  async function checkPending() {
    const { ok, body } = await api("/api/v1/games/pending");
    if (!ok) return;
    const pending = body.pending || {};
    if (pending.game_id) {
      $("games-pending").hidden = false;
      $("games-pending-text").textContent = `You opened a link to “${pending.title || "a game"}”. Its setup is below; nothing has started.`;
      if (!current || current.id !== pending.game_id) await openGame(pending.game_id);
      return;
    }
    if (pending.problem || pending.waiting) {
      $("games-pending").hidden = false;
      $("games-pending-text").textContent =
        body.code === "sign_in_required" ? "You opened a join link. Sign in to see the game it is for." : pending.problem;
      return;
    }
    $("games-pending").hidden = true;
  }

  // --- wiring ----------------------------------------------------------------------

  $("games-sign-in").addEventListener("click", (event) => window.AUCOM.openSignIn(event.currentTarget));
  $("games-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, () => loadList()));
  $("games-more").addEventListener("click", (event) => withBusy(event.currentTarget, () => loadList({ more: true })));
  for (const id of ["games-scope", "games-family", "games-mode"]) {
    $(id).addEventListener("change", () => loadList());
  }
  $("games-detail-close").addEventListener("click", () => {
    $("games-detail").hidden = true;
    current = null;
    window.history.replaceState(null, "", "#games");
  });
  $("games-recheck").addEventListener("click", (event) => withBusy(event.currentTarget, () => current && openGame(current.id)));
  $("games-primary").addEventListener("click", (event) => primaryAction(event.currentTarget));
  $("games-pending-dismiss").addEventListener("click", async () => {
    await api("/api/v1/games/pending/dismiss", { method: "POST", body: {} });
    $("games-pending").hidden = true;
  });

  window.AUCOM.areas.games = {
    refresh: async (argument) => {
      await window.AUCOM.refreshStatus?.();
      if (!signedIn()) {
        showSignedOut(true);
        await checkPending();
        return;
      }
      await loadList();
      await checkPending();
      if (argument && (!current || current.id !== argument)) await openGame(argument);
    },
  };
})();
