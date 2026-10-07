// The page's lease on the Companion, and Quit.
//
// While this page is open it holds one WebSocket to the Companion. That is how
// an interactive Companion — the one a person started by opening the program —
// knows a page is still open: when the last lease has been gone for the grace
// period, and nothing is running, it stops. A reload drops the lease and the
// new page takes one a moment later; that is what the grace period is for.
// Nothing here listens for `beforeunload`, which cannot tell a reload from a
// close. See internal/web/lifecycle.go.
//
// The token goes in the WebSocket's subprotocol list, because a page cannot set
// a header on a WebSocket: still a header, never the URL. It is this run's API
// token, the same one every other call sends. See internal/web/lease.go.

"use strict";

(() => {
  const { $, el, api, setMessage, withBusy, record, t } = window.AUCOM;

  const LEASE_PROTOCOL = "aucom.lease.v1";
  const BEAT_MS = 10000;
  // A Companion that has said nothing for this long is treated as gone, and the
  // page reconnects. The server beats every 10 s.
  const SILENCE_MS = 35000;

  let socket = null;
  let lastHeard = 0;
  let failures = 0;
  let stopped = false;
  let stale = false;

  const causeText = {
    quit: "Quit was chosen.",
    ui_closed: "Its last window was closed.",
    interrupted: "It was stopped from the terminal or by the system.",
    startup_timeout: "No page connected to it in time.",
    work_finished: "The work it was finishing has ended and no page was open.",
  };

  function showStopped(cause) {
    if (stopped) return;
    stopped = true;
    // Every other dialog goes: nothing in one can work now, and one left open
    // showed around the stopped notice (NEW_310, the leak-test pipeline dialog
    // on Windows).
    for (const modal of document.querySelectorAll(".auth-modal")) {
      if (modal.id !== "stopped-modal") modal.hidden = true;
    }
    for (const dialog of document.querySelectorAll("dialog[open]")) dialog.close();
    $("stopped-text").textContent = t(causeText[cause] || "It is no longer answering.");
    $("stopped-modal").hidden = false;
    document.title = t("Stopped — Auto-Pigeon Companion");
  }

  function connect() {
    if (stopped || stale) return;
    const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
    let ws;
    try {
      ws = new WebSocket(`${scheme}//${window.location.host}/api/lifecycle/lease`,
        [LEASE_PROTOCOL, "aucom.token." + apiToken]);
    } catch {
      retry();
      return;
    }
    socket = ws;
    let stoppingCause = null;
    ws.addEventListener("open", () => {
      failures = 0;
      lastHeard = Date.now();
    });
    ws.addEventListener("message", (event) => {
      lastHeard = Date.now();
      let message = {};
      try {
        message = JSON.parse(event.data);
      } catch {
        return;
      }
      if (message.type === "hello") {
        window.AUCOM.lifecycle = { mode: message.mode, graceSeconds: message.grace_seconds };
      } else if (message.type === "stopping") {
        stoppingCause = message.cause || "quit";
      }
    });
    ws.addEventListener("close", () => {
      if (socket === ws) socket = null;
      if (stoppingCause) {
        showStopped(stoppingCause);
        return;
      }
      retry();
    });
  }

  // retry reconnects quickly — a Companion that is still there answers at
  // once — and after a few failures asks the API why, because a WebSocket
  // handshake that failed says nothing about the reason.
  async function retry() {
    if (stopped || stale) return;
    failures += 1;
    if (failures >= 3) {
      const { status, body } = await api("/api/status");
      if (status === 401 && body.code === "token_refused") {
        // The Companion restarted; api() has shown the Reload banner, and
        // this page's token will never be accepted again.
        stale = true;
        return;
      }
      if (status === 0 && failures >= 6) {
        showStopped("");
        return;
      }
    }
    window.setTimeout(connect, Math.min(400 * failures, 3000));
  }

  window.setInterval(() => {
    if (!socket || socket.readyState !== WebSocket.OPEN) return;
    try {
      socket.send('{"type":"beat"}');
    } catch {
      // the close event follows
    }
    if (Date.now() - lastHeard > SILENCE_MS) socket.close();
  }, BEAT_MS);

  // --- Quit ---------------------------------------------------------------

  let pending = [];

  function drawActive(active) {
    const list = $("quit-list");
    list.replaceChildren();
    for (const item of active) {
      list.append(el("li", {
        children: [
          el("span", { text: item.label }),
          el("span", { className: "badge queued", text: item.state || item.kind.replace(/_/g, " ") }),
        ],
      }));
    }
  }

  async function openQuit(opener) {
    $("quit-modal").hidden = false;
    $("quit-confirm").disabled = true;
    setMessage("quit-message", "");
    $("quit-intro").textContent = t("Checking what is still running…");
    $("quit-list").replaceChildren();
    $("quit-keep").focus();
    window.AUCOM.quitOpener = opener;
    const { ok, body } = await api("/api/lifecycle");
    if (!ok) {
      $("quit-intro").textContent = "";
      setMessage("quit-message", body.error || t("could not ask the Companion what is running"), "error");
      return;
    }
    pending = body.active || [];
    drawActive(pending);
    if (pending.length) {
      $("quit-intro").textContent = t("{n} thing(s) are still running. Quitting cancels them; anything half-installed is removed.", { n: pending.length });
      $("quit-confirm").textContent = t("Cancel them and quit");
    } else {
      $("quit-intro").textContent = t("Nothing is running. Quitting stops the Companion; start it again to continue.");
      $("quit-confirm").textContent = t("Quit");
    }
    $("quit-confirm").disabled = false;
  }

  function closeQuit() {
    $("quit-modal").hidden = true;
    window.AUCOM.quitOpener?.focus?.();
  }

  $("quit-confirm").addEventListener("click", (event) =>
    withBusy(event.currentTarget, async () => {
      setMessage("quit-message", pending.length ? t("Cancelling, then stopping…") : t("Stopping…"), "busy");
      const { ok, status, body } = await api("/api/lifecycle/quit", {
        method: "POST",
        body: { cancel_active: pending.length > 0 },
      });
      if (status === 409 && body.code === "work_active") {
        // Something started between opening this dialog and pressing Quit.
        pending = body.active || [];
        drawActive(pending);
        $("quit-intro").textContent = t("Something else started. {n} thing(s) are running now.", { n: pending.length });
        $("quit-confirm").textContent = t("Cancel them and quit");
        setMessage("quit-message", "");
        return;
      }
      if (!ok) {
        setMessage("quit-message", body.error || t("the Companion did not stop"), "error");
        return;
      }
      record("Quit Auto-Pigeon Companion", pending.length ? t("{n} cancelled", { n: pending.length }) : "", "ok");
      // The lease's "stopping" message shows the stopped screen; this is for
      // the moment between.
      setMessage("quit-message", t("Stopping…"), "busy");
    })
  );
  $("quit-keep").addEventListener("click", closeQuit);
  $("quit-close").addEventListener("click", closeQuit);
  document.querySelector('[data-dismiss="quit"]').addEventListener("click", closeQuit);
  $("quit-modal").addEventListener("keydown", (event) => {
    if (event.key === "Escape") closeQuit();
  });

  window.AUCOM.openQuit = openQuit;
  connect();
})();
