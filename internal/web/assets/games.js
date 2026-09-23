// The Games area — AUB/AUG/AUCOM/AUT 244F.
//
// Everything this area says about whether somebody can join comes from one
// readiness report the server computes (internal/joinready). The page renders
// it and offers the ONE next action the report names; it never decides "ready"
// itself.
//
// One small card per game and one way to join it (operator, 2026-09-23: "why do
// I see a second messy card in the bottom, giving me 2 different ways to join?").
// The card's button is Join when this computer can play the game, and Setup —
// the engine's page in Profiles, where setup happens and nowhere else — when it
// cannot yet. Join is one press: it downloads and checks the map files, spends a
// fresh link, re-checks the game and starts the engine; what happened is written
// on the card, and the exact command is in Jobs.
//
// A clicked autopigeon:// link arrives here as a pending game: the server
// redeems it once, and this area shows that game's card first.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, announce, record, badge, bytes, t } = window.AUCOM;

  let cursor = "";
  let engines = []; // this computer's engine profiles, for each card's button
  const running = new Set(); // games this window started

  function signedIn() {
    return Boolean(window.AUCOM.status?.authenticated);
  }

  function showSignedOut(visible) {
    $("games-signed-out").hidden = !visible;
    $("games-list-panel").hidden = visible;
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
    const [listing, known] = await Promise.all([api("/api/v1/games?" + query.toString()), api("/api/v1/engines")]);
    if (known.ok) engines = known.body.items || [];
    const { ok, status, body } = listing;
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
    for (const game of games) $("games-list").append(gameCard(game));
    setMessage(
      "games-message",
      $("games-list").children.length === 0
        ? $("games-scope").value === "mine"
          ? "You are not hosting anything, and have not recently."
          : "Nobody is hosting a public game right now."
        : ""
    );
  }

  // The engine profile on this computer that would play a game, as far as the
  // listing can tell: one implementing the game's runtime, a ready one first.
  // The readiness report decides when Join is pressed.
  function engineFor(runtime) {
    const matching = engines.filter((engine) => String(engine.runtime || "").toLowerCase() === String(runtime || "").toLowerCase());
    return matching.find((engine) => engine.ready) || matching[0] || null;
  }

  // One small card: the facts on four short lines, and the button bottom right.
  function gameCard(game) {
    const live = game.state === "live";
    const head = el("div", { className: "row-head", children: [
      el("strong", { className: "game-title", text: game.title }),
      badge(live ? "live" : "ended", live ? "running" : "cancelled"),
    ] });
    const lines = [
      game.host_nickname ? `Hosted by ${game.host_nickname}` : "",
      [game.map_name, engineName(game)].filter(Boolean).join(" · "),
      players(game).text,
      [game.join_content?.state === "required" ? bytes(game.join_content.total_bytes) : "", freshness(game.stale_for_ms)]
        .filter(Boolean).join(" · "),
    ].filter(Boolean).map((text) => el("p", { className: "muted game-fact", text, attrs: text === players(game).text ? { title: players(game).title } : {} }));
    const status = el("p", { className: "message small game-status", attrs: { role: "status" } });
    const button = el("button", { attrs: { type: "button" } });
    drawButton(game, button);
    button.addEventListener("click", () => act(game, button, status));
    return el("li", {
      className: "card game-card",
      attrs: { "data-game": game.id },
      children: [head, ...lines, status, el("div", { className: "row-actions", children: [button] })],
    });
  }

  function drawButton(game, button) {
    const live = game.state === "live";
    button.disabled = false;
    button.title = "";
    if (!live) {
      button.textContent = "Join";
      button.className = "primary";
      button.disabled = true;
      button.title = "This game has ended.";
      return;
    }
    if (running.has(game.id)) {
      button.textContent = "Running";
      button.className = "primary";
      button.disabled = true;
      button.title = "This game is running on this computer; see Jobs to follow or stop it.";
      return;
    }
    if (!game.joinable) {
      button.textContent = "Join";
      button.className = "primary";
      button.disabled = true;
      button.title = "This game cannot be joined from this account.";
      return;
    }
    const engine = engineFor(game.engine_runtime);
    if (!engine || !engine.ready) {
      button.textContent = "Setup";
      button.className = "primary";
      button.title = engine ? `${engine.name} needs setting up on this computer.` : "No engine on this computer plays this game yet.";
      return;
    }
    button.textContent = "Join";
    button.className = "primary";
  }

  function engineName(game) {
    const runtime = String(game.engine_runtime || "");
    const known = { vkquake: "vkQuake", quakespasm: "QuakeSpasm", ironwail: "Ironwail", darkplaces: "DarkPlaces", fteqw: "FTEQW" };
    const name = known[runtime.toLowerCase()] || runtime;
    return [name, game.engine_version].filter(Boolean).join(" ");
  }

  // The engine's own count when it reports one; otherwise how many joined
  // through Auto-Pigeon, which AUB counts — and says which it is.
  function players(game) {
    if (game.players_observable) {
      return { text: `${game.players_current}/${game.players_max} players`, title: "Connected now, as the host's engine reports it." };
    }
    const joined = Number(game.joined || 0);
    return {
      text: `${joined} joined`,
      title: "How many people joined through Auto-Pigeon. The host's engine does not report who is connected now.",
    };
  }

  function freshness(ms) {
    const seconds = Math.round((ms || 0) / 1000);
    if (seconds < 5) return "just now";
    if (seconds < 120) return `${seconds} seconds ago`;
    return `${Math.round(seconds / 60)} minutes ago`;
  }

  // --- the one action ---------------------------------------------------------------

  async function readiness(id) {
    const { ok, status, body } = await api("/api/v1/games/" + encodeURIComponent(id));
    if (!ok && status === 401) showSignedOut(true);
    return { ok, body };
  }

  // Setup is the engine's page in Profiles, with Profiles lit in the side menu.
  function openSetup(profileID) {
    window.AUCOM.showArea(profileID ? "profiles/" + encodeURIComponent(profileID) : "profiles");
  }

  async function act(game, button, status) {
    if (button.textContent === "Setup") {
      openSetup(engineFor(game.engine_runtime)?.id);
      return;
    }
    await withBusy(button, async () => {
      for (let guard = 0; guard < 3; guard++) {
        busy(status, "Checking what this computer needs…");
        const { ok, body } = await readiness(game.id);
        if (!ok) {
          setMessage(status, body.error || "This game could not be read.", "error");
          return;
        }
        const report = body.readiness;
        if (report.state === "sign_in_required") {
          setMessage(status, "");
          window.AUCOM.openSignIn(button);
          return;
        }
        if (report.state === "ready_for_review") {
          await launch(game, report, status);
          return;
        }
        const next = (report.steps || []).find((step) => step.id === report.next);
        if (next?.action === "download_join_content") {
          busy(status, "Downloading this game's map files and checking every one…");
          const content = await api(`/api/v1/games/${encodeURIComponent(game.id)}/content`, { method: "POST", body: {} });
          if (!content.ok) {
            setMessage(status, content.body.error || "The map files could not be downloaded.", "error");
            return;
          }
          continue;
        }
        if (next && ["install_engine_profile", "approve_engine_profile", "choose_engine_executable", "choose_game_folder"].includes(next.action)) {
          // Setup, not a join step: it happens in Profiles.
          setMessage(status, next.detail || next.title, "error");
          openSetup(report.engine?.profile_id);
          return;
        }
        setMessage(status, next ? next.detail || next.title : report.title || "This game cannot be joined now.", "error");
        return;
      }
    });
    drawButton(game, button);
  }

  async function launch(game, report, status) {
    busy(status, "Joining: checking the game has not changed and starting the engine…");
    const review = await api(`/api/v1/games/${encodeURIComponent(game.id)}/review`, { method: "POST", body: {} });
    if (!review.ok) {
      setMessage(status, review.body.error || "The join could not be prepared.", "error");
      return;
    }
    const plan = review.body;
    const { ok, body } = await api(`/api/v1/games/${encodeURIComponent(game.id)}/launch`, {
      method: "POST",
      body: { plan_id: plan.plan_id, approve: true },
    });
    if (!ok) {
      setMessage(status, body.error || "The game could not be started.", "error");
      return;
    }
    running.add(game.id);
    const message = body.already
      ? "Already running on this computer; see Jobs."
      : `${plan.engine} is starting and connecting to ${plan.endpoint}. See Jobs for the exact command.`;
    setMessage(status, message, "ok");
    announce(message);
    record(body.already ? "Join already running" : "Joined a game", report.title || game.title, "ok");
  }

  // --- a link the operating system delivered ------------------------------------------

  async function checkPending() {
    const { ok, body } = await api("/api/v1/games/pending");
    if (!ok) return;
    const pending = body.pending || {};
    if (pending.game_id) {
      $("games-pending").hidden = false;
      $("games-pending-text").textContent = `You opened a link to “${pending.title || "a game"}”. It is the first card below; nothing has started.`;
      await showFirst(pending.game_id);
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

  // showFirst puts one game's card first: the one a link or `#games/<id>`
  // names, even when it is not in the list being shown.
  async function showFirst(id) {
    const list = $("games-list");
    let card = list.querySelector(`[data-game="${CSS.escape(id)}"]`);
    if (!card) {
      const { ok, body } = await api("/api/v1/games/" + encodeURIComponent(id));
      const game = ok ? body.game : null;
      if (!game) return;
      card = gameCard(game);
    }
    list.prepend(card);
    card.scrollIntoView({ block: "nearest" });
    card.classList.add("game-card--named");
  }

  // --- wiring ----------------------------------------------------------------------

  $("games-sign-in").addEventListener("click", (event) => window.AUCOM.openSignIn(event.currentTarget));
  $("games-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, () => loadList()));
  $("games-more").addEventListener("click", (event) => withBusy(event.currentTarget, () => loadList({ more: true })));
  for (const id of ["games-scope", "games-family", "games-mode"]) {
    $(id).addEventListener("change", () => loadList());
  }
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
      if (argument) await showFirst(argument);
    },
  };
})();
