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
  let reviewing = null; // the plan id awaiting approval

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

  function gameRow(game) {
    const live = game.state === "live";
    const head = el("div", { className: "row-head" });
    const title = el("strong", { text: game.title });
    head.append(title);
    head.append(badge(live ? "live" : "ended", live ? "running" : "cancelled"));
    const facts = [
      game.host_nickname ? `Hosted by ${game.host_nickname}` : "",
      game.map_name ? `Map: ${game.map_name}` : "",
      familyNames[game.game_family] || game.game_family,
      players(game),
      `heard from ${freshness(game.stale_for_ms)}`,
    ].filter(Boolean);
    const line = el("p", { className: "muted", text: facts.join(" · ") });
    const content = contentLine(game.join_content);
    const open = el("button", { text: live && game.joinable ? "Open" : "View", attrs: { type: "button" } });
    open.addEventListener("click", () => openGame(game.id));
    return el("li", {
      children: [head, line, el("p", { className: "muted", text: content }), el("div", { className: "row-actions", children: [open] })],
    });
  }

  function players(game) {
    if (!game.players_observable) return "players: not reported by the host's server";
    return `${game.players_current} of ${game.players_max} players, as the host reports it`;
  }

  function freshness(ms) {
    const seconds = Math.round((ms || 0) / 1000);
    if (seconds < 5) return "just now";
    if (seconds < 120) return `${seconds} seconds ago`;
    return `${Math.round(seconds / 60)} minutes ago`;
  }

  function contentLine(content) {
    switch (content?.state) {
      case "required":
        return `Map files to download: ${bytes(content.total_bytes)}`;
      case "not_required":
        return "No map files needed beyond your own game, the host says";
      default:
        return "The host did not say which map files this game needs";
    }
  }

  // --- one game ----------------------------------------------------------------

  async function openGame(id, { quiet = false } = {}) {
    if (!id) return;
    if (window.location.hash !== "#games/" + id) {
      window.history.replaceState(null, "", "#games/" + id);
    }
    $("games-detail").hidden = false;
    $("games-action").hidden = true;
    $("games-action").replaceChildren();
    reviewing = null;
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

    const facts = $("games-facts");
    facts.replaceChildren();
    const fact = (term, value) => {
      if (!value) return;
      facts.append(el("dt", { text: term }), el("dd", { text: value }));
    };
    fact("Hosted by", readiness.host);
    fact("Map", readiness.map_name);
    fact("Engine", readiness.engine?.name);
    fact("Players", readiness.players);
    fact("Address", readiness.endpoint ? `${readiness.endpoint} (${reachability(readiness.details?.reachability)})` : "not shown to your account");
    fact("Heard from the host", readiness.freshness);
    if (readiness.package_bytes) fact("Map files", bytes(readiness.package_bytes));

    const steps = $("games-steps");
    steps.replaceChildren();
    for (const step of readiness.steps || []) {
      const item = el("li", { className: "join-step" + (step.done ? " done" : "") + (step.id === readiness.next ? " next" : "") });
      const head = el("div", { className: "row-head" });
      head.append(el("strong", { text: step.title }));
      head.append(badge(step.done ? "done" : step.id === readiness.next ? "next" : "waiting", step.done ? "ok" : step.id === readiness.next ? "warning" : "cancelled"));
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
    switch (readiness.state) {
      case "ready_for_review":
        primary.textContent = "Join";
        break;
      case "setup_required":
        // Downloading the map files is part of JOINING, not of setting this
        // computer up (operator, 2026-09-23): when that is all that is left,
        // the one button says Join, and one press downloads and goes on to the
        // review.
        primary.textContent = nextStep()?.action === "download_join_content" ? "Join" : "Set up to join";
        break;
      case "sign_in_required":
        primary.textContent = "Sign in";
        break;
      default:
        primary.textContent = "Join";
        primary.disabled = true;
    }
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
    if (readiness.state === "ready_for_review") {
      await review(button);
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
    lines.push(el("h4", { text: "What it may do on this computer" }), el("pre", { className: "output", text: review.report }));
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
        el("h4", { text: `What ${body.name} may do on this computer` }),
        el("pre", { className: "output", text: body.report }),
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
    // The press was "Join": once the files are in, go straight on to the review
    // of the exact command, which still starts nothing until it is approved.
    if (downloaded && current?.readiness?.state === "ready_for_review") await review(button);
  }

  // --- the review, and the one start -----------------------------------------------

  async function review(button) {
    await withBusy(button, async () => {
      busy("games-detail-message", "Getting a fresh link and checking the game has not changed…");
      const { ok, body } = await api(`/api/v1/games/${encodeURIComponent(current.id)}/review`, { method: "POST", body: {} });
      if (!ok) {
        if (body.readiness) render(current.id, body.readiness);
        setMessage("games-detail-message", body.error || "The join could not be prepared.", "error");
        return;
      }
      setMessage("games-detail-message", "");
      reviewing = body.plan_id;
      const start = el("button", { className: "primary", text: "Start the game", attrs: { type: "button" } });
      const cancel = el("button", { className: "secondary", text: "Cancel", attrs: { type: "button" } });
      const children = [
        el("h4", { text: "This is exactly what will run" }),
        el("pre", { className: "output", text: body.preview?.shell || "" }),
        el("p", { className: "muted", text: `in ${body.preview?.working_dir || ""}` }),
        el("p", {
          className: "muted",
          text: `${body.engine} will connect to ${body.endpoint}${body.map_files ? ", loading the map files downloaded for this game" : ""}. It runs as a job you can stop from Jobs.`,
        }),
      ];
      for (const warning of body.warnings || []) children.push(el("p", { className: "join-warning", text: warning }));
      children.push(
        el("p", { className: "muted", text: `This review is valid for ${Math.round((body.expires_in || 120) / 60)} minutes.` }),
        el("div", { className: "row-actions", children: [start, cancel] })
      );
      actionPanel(children);
      cancel.addEventListener("click", () => {
        reviewing = null;
        $("games-action").hidden = true;
      });
      start.addEventListener("click", () => launch(start));
      start.focus();
    });
  }

  async function launch(button) {
    if (!reviewing) return;
    const planID = reviewing;
    await withBusy(button, async () => {
      busy("games-detail-message", "Starting…");
      const { ok, body } = await api(`/api/v1/games/${encodeURIComponent(current.id)}/launch`, {
        method: "POST",
        body: { plan_id: planID, approve: true },
      });
      reviewing = null;
      if (!ok) {
        setMessage("games-detail-message", body.error || "The game could not be started.", "error");
        return;
      }
      const message = body.already ? "This game is already running; see Jobs." : "The game is starting. It is running as a job; see Jobs to follow or stop it.";
      const jobs = el("button", { className: "secondary", text: "Open Jobs", attrs: { type: "button" } });
      jobs.addEventListener("click", () => window.AUCOM.showArea("jobs"));
      actionPanel([el("p", { text: message }), el("div", { className: "row-actions", children: [jobs] })]);
      setMessage("games-detail-message", message, "ok");
      record(body.already ? "Join already running" : "Joined a game", current.readiness.title, "ok");
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
