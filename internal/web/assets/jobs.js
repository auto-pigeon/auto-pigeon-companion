// The Jobs area: everything that has run, with the command, the exit status and
// the output.
//
// Retry is spelled out rather than implied. A retried job is a NEW job with the
// same request, and the page says so — a button labelled "retry" that quietly
// re-ran something is how a build gets run twice by somebody who thought they
// were looking at the first one.
//
// A job's page is `#jobs/<id>`, so a job's name in the list is a real link to
// it (NEW_265): it can be focused with Tab, opened with Enter, and a reload
// comes back to the same job.

"use strict";

(() => {
  const { $, el, api, setMessage, withBusy, record, badge, when, terminal, duration, t } = window.AUCOM;

  let watching = null;
  let poller = null;
  // The open job's page: its summary is redrawn only when the record changes,
  // and its output view is created once and only ever appended to — a detail
  // rebuilt every second threw away the reader's scroll, selection and focus.
  let view = null;
  let summaryKey = "";

  async function refreshJobs() {
    const list = $("jobs-list");
    const state = $("jobs-state").value;
    const query = new URLSearchParams({ limit: "40" });
    if (state) query.set("state", state);
    const { ok, body } = await api("/api/v1/jobs?" + query.toString());
    if (!ok) {
      setMessage("jobs-message", body.error || "could not read the jobs", "error");
      return;
    }
    // Focus survives a refresh of the list: the row a person had tabbed to
    // is found again by its job, not lost to a redraw.
    const focused = document.activeElement?.closest?.("#jobs-list [data-job]");
    const focusKey = focused ? focused.dataset.job + "|" + (document.activeElement.dataset.control || "") : "";
    list.replaceChildren();
    const jobs = body.items || [];
    setMessage("jobs-message", jobs.length === 0 ? "No jobs yet." : `${jobs.length} job(s).`);
    for (const job of jobs) list.append(jobRow(job));
    if (focusKey) {
      const [id, control] = focusKey.split("|");
      list.querySelector(`[data-job="${CSS.escape(id)}"] [data-control="${CSS.escape(control)}"]`)?.focus();
    }
  }

  function jobName(job) {
    const what = job.action_title || job.action_id || "a job";
    const who = job.profile_name || "";
    return job.label || (who ? `${what} · ${who}` : what);
  }

  function jobHref(id) {
    return "#jobs/" + encodeURIComponent(id);
  }

  // How long a job took, or has been running as of this reading (NEW_345: a
  // row said when a job started and never how long it ran). Read from the
  // record's own times; nothing here ticks between refreshes.
  function took(job) {
    const start = Date.parse(job.started_at || "");
    if (!Number.isFinite(start)) return "";
    const end = terminal(job.state) ? Date.parse(job.finished_at || "") : Date.now();
    if (!Number.isFinite(end) || end < start) return "";
    return terminal(job.state) ? t("took {time}", { time: duration(end - start) }) : t("running for {time}", { time: duration(end - start) });
  }

  function jobRow(job) {
    const head = el("div", { className: "row-head" });
    // Named by what ran, never by the job's id (operator, NEW_244D). The name
    // is the link to the job's page: an <a href>, so it is reachable and
    // opened from the keyboard like any link, and it is a sibling of the row's
    // buttons rather than a container around them.
    head.append(el("a", {
      className: "job-row__title",
      text: jobName(job),
      attrs: { href: jobHref(job.id), "data-control": "title" },
    }));
    head.append(badge(job.state));
    const detail = el("p", { className: "muted" });
    detail.textContent = [
      job.exit_code !== undefined && job.exit_code !== null ? "exit " + job.exit_code : "",
      took(job),
      job.started_at ? when(job.started_at) : "",
    ]
      .filter(Boolean)
      .join(" · ");

    // The name is the link to the job's page (NEW_365: the identical Open
    // button beside it went). A failed job says what kind of failure, a
    // cancelled or interrupted one says so in its own word, and only a running
    // job has a control in its row: Stop.
    if (job.state === "failed" && (job.failure_class || job.timed_out)) {
      detail.textContent += (detail.textContent ? " · " : "") + (job.timed_out ? t("timed out") : job.failure_class);
    }
    const actions = el("div", { className: "row-actions" });
    if (!terminal(job.state)) {
      const cancel = el("button", { text: "Stop", attrs: { type: "button", class: "danger", "data-control": "stop" } });
      cancel.addEventListener("click", () =>
        withBusy(cancel, async () => {
          const { ok, body } = await api(`/api/v1/jobs/${encodeURIComponent(job.id)}/cancel`, { method: "POST" });
          if (!ok) {
            setMessage("jobs-message", body.error, "error");
            return;
          }
          record(`Stopped ${jobName(job)}`, "", "cancelled");
          await afterStop(job.id);
        })
      );
      actions.append(cancel);
    }
    // One line per job: what ran and when on the left, its state and its
    // controls on the right (the rows were three lines each).
    return el("li", {
      className: "job-row",
      attrs: { "data-job": job.id },
      children: [el("div", { className: "job-row__text", children: [head, detail] }), actions],
    });
  }

  // A cancel is a request: the process is signalled and the job is recorded
  // cancelled a moment later. Refreshing straight away caught it still
  // `running`, with its Stop button, until somebody pressed Refresh (246I1.1,
  // seen live). So wait — briefly, and bounded — for the state to change.
  async function afterStop(id) {
    const deadline = Date.now() + 10000;
    while (Date.now() < deadline) {
      const { ok, body } = await api("/api/v1/jobs/" + encodeURIComponent(id));
      if (!ok || terminal(body.state)) break;
      await new Promise((resolve) => setTimeout(resolve, 300));
    }
    await refreshJobs();
    if (watching === id) await tick();
  }

  async function openJob(id) {
    if (poller) window.clearTimeout(poller);
    watching = id;
    summaryKey = "";
    const panel = $("job-detail-panel");
    const detail = $("job-detail");
    panel.hidden = false;
    view = window.AUCOM.outputView({ label: t("Output"), sourcePicker: true });
    view.follow(id);
    detail.replaceChildren(
      el("div", { attrs: { id: "job-detail-summary" }, children: [el("p", { className: "muted", text: "Reading…" })] }),
      el("div", { attrs: { id: "job-detail-actions" } }),
      view.root,
    );
    $("job-detail-title").setAttribute("tabindex", "-1");
    $("job-detail-title").focus();
    await tick();
  }

  // tick is one poll of the open job: its record, and the next of its output.
  async function tick() {
    if (poller) window.clearTimeout(poller);
    const id = watching;
    if (!id) return;
    const { ok, body } = await api("/api/v1/jobs/" + encodeURIComponent(id));
    if (id !== watching) return;
    if (!ok) {
      // Said beside what is on screen, not instead of it.
      view?.setError(body.error || "the job could not be read");
      poller = window.setTimeout(tick, 2000);
      return;
    }
    renderSummary(body);
    const read = await view.poll();
    if (id !== watching) return;
    if (terminal(body.state) && (read.complete || read.error)) {
      if (!read.error) {
        await refreshJobs();
        return;
      }
    }
    poller = window.setTimeout(tick, terminal(body.state) ? 1500 : 1000);
  }

  // The parts of a job record the summary shows. When none of them changed, the
  // summary is left exactly as it is — including a focused Stop button.
  function keyOf(body) {
    return JSON.stringify([
      body.state, body.exit_code, body.error, body.timed_out, body.started_at, body.finished_at,
      body.failure_class, (body.diagnostics || []).length, (body.artifacts || []).map((a) => [a.name, a.missing]),
      body.command?.shell, body.custom_args,
    ]);
  }

  // The job's page: what ran and how it ended in the open — state, times,
  // the live line while it runs, what went wrong, findings and outputs — and
  // the exact command, program, folders and raw facts in Technical details,
  // closed until somebody opens it (NEW_365).
  function renderSummary(body) {
    renderLive(body);
    const key = keyOf(body);
    if (key === summaryKey) return;
    summaryKey = key;
    $("job-detail-title").textContent = jobName(body);
    const summary = $("job-detail-summary");
    const wasOpen = summary.querySelector("details.job-technical")?.open || false;
    const parts = [];

    const head = el("p");
    head.append(badge(body.state));
    head.append(document.createTextNode(" " + [took(body), body.started_at ? t("started {when}", { when: when(body.started_at) }) : ""]
      .filter(Boolean).join(" · ")));
    parts.push(head);
    const live = el("p", { className: "mono job-live", attrs: { id: "job-live" } });
    parts.push(live);

    if (body.error) {
      parts.push(el("p", { className: "message error", text: body.error }));
    }

    if ((body.diagnostics || []).length) {
      parts.push(el("h4", { text: "Findings" }));
      const list = el("ul");
      for (const diagnostic of body.diagnostics) {
        const row = el("li", { children: [badge(diagnostic.severity, diagnostic.severity === "error" ? "failed" : "queued"), document.createTextNode(" " + diagnostic.message)] });
        // The program's own line beside the rule's sentence, and the finding's
        // kind: "a model could not be opened" does not say WHICH model.
        if (diagnostic.class) row.append(el("span", { className: "stage-detail", text: " [" + diagnostic.class + "]" }));
        if (diagnostic.raw && diagnostic.raw !== diagnostic.message) row.append(el("pre", { className: "output", text: diagnostic.raw }));
        list.append(row);
      }
      parts.push(list);
    }

    if ((body.artifacts || []).length) {
      parts.push(el("h4", { text: "Artifacts" }));
      const list = el("ul");
      for (const artifact of body.artifacts) {
        const item = el("li");
        if (artifact.missing) {
          item.append(document.createTextNode(`${artifact.name}: the job declared it and it is not there`));
        } else {
          item.append(
            window.AUCOM.downloadButton(
              artifact.name,
              `/api/v1/jobs/${encodeURIComponent(body.id)}/artifacts/${encodeURIComponent(artifact.name)}`,
              artifact.name
            )
          );
        }
        list.append(item);
      }
      parts.push(list);
    }

    // Technical details: everything needed to decide what ran, exactly.
    const facts = [];
    const fact = (term, value) => { if (value) facts.push(el("dt", { text: term }), el("dd", { className: "mono", text: value })); };
    fact(t("Command"), body.command?.shell);
    if ((body.custom_args || []).length) {
      facts.push(el("dt", { text: t("Your own arguments:") }), el("dd", { children: [
        ...body.custom_args.map((token) => el("code", { className: "custom-arg", text: token })),
        el("span", { className: "muted", text: " " + t("(from this program's setup in Profiles)") }),
      ] }));
    }
    fact(t("Program"), body.command?.executable);
    fact(t("Working directory"), body.command?.working_dir);
    fact(t("Command digest"), body.command?.digest);
    fact(t("Profile"), [body.profile_name, body.profile_id, body.profile_version, body.profile_digest].filter(Boolean).join(" · "));
    fact(t("Exit status"), body.exit_code !== undefined && body.exit_code !== null ? String(body.exit_code) : "");
    fact(t("Timed out"), body.timed_out ? t("yes, after {n} s", { n: body.timeout_seconds || "?" }) : "");
    fact(t("Kind of failure"), body.failure_class);
    fact(t("Started"), body.started_at ? when(body.started_at) : "");
    fact(t("Finished"), body.finished_at ? when(body.finished_at) : "");
    fact(t("Process"), body.process?.pid ? `pid ${body.process.pid}` : "");
    fact(t("Job"), body.id);
    for (const artifact of body.artifacts || []) fact(artifact.name, artifact.path ? `${artifact.path}${artifact.sha256 ? " · " + artifact.sha256 : ""}` : "");
    const technical = el("details", { className: "job-technical", children: [
      el("summary", { text: t("Technical details") }),
      el("dl", { className: "summary-list", children: facts }),
      el("p", { className: "muted small", text: t("Credentials the Companion knows about are removed from this view. The raw bytes are kept beside the job.") }),
    ] });
    technical.open = wasOpen;
    parts.push(technical);
    summary.replaceChildren(...parts);
    renderLive(body);
    renderActions(body);
  }

  // The live line is redrawn as text every poll, never as part of the summary,
  // so it ticks while a focused Stop or an open Technical details stays put.
  function renderLive(body) {
    const node = document.getElementById("job-live");
    if (!node) return;
    const running = !terminal(body.state);
    const line = window.AUCOM.liveLine(body.live, { stage: body.action_title || "", running });
    node.textContent = line.text;
    node.title = line.title;
    node.hidden = !line.text;
  }

  async function renderActions(body) {
    const holder = $("job-detail-actions");
    const actions = el("div", { className: "row-actions" });
    if (terminal(body.state)) {
      const retry = el("button", { text: "Run this again", attrs: { type: "button", class: "secondary" } });
      retry.addEventListener("click", () =>
        withBusy(retry, async () => {
          const { ok, body: out } = await api(`/api/v1/jobs/${encodeURIComponent(body.id)}/retry`, { method: "POST" });
          if (!ok) {
            holder.append(el("p", { className: "message error", text: out.error }));
            return;
          }
          record(`Ran ${jobName(body)} again`, "", "running");
          await refreshJobs();
          window.location.hash = jobHref(out.id);
        })
      );
      actions.append(retry);
      // A failed job this Companion reported as an incident can be reported
      // as a bug about exactly that incident (NEW_247H): the dialog starts on
      // the contract's type and area for it, and the person can change both.
      if (body.state === "failed") {
        const raised = await api("/api/v1/bug-reports/incidents");
        const about = raised.ok ? (raised.body.incidents || []).find((entry) => entry.job_id === body.id) : null;
        if (about && window.AUCOM.reportBug) {
          const report = el("button", { text: t("Report a bug about this failure"), attrs: { type: "button", id: "job-report-bug" } });
          report.addEventListener("click", () => window.AUCOM.reportBug({ incident: about }));
          actions.append(report);
        }
      }
      retry.title = t("Makes a NEW job with the same request; this one stays as it is.");
    } else {
      const stop = el("button", { text: "Stop", attrs: { type: "button", class: "danger" } });
      stop.addEventListener("click", () =>
        withBusy(stop, async () => {
          const { ok, body: out } = await api(`/api/v1/jobs/${encodeURIComponent(body.id)}/cancel`, { method: "POST" });
          if (!ok) {
            holder.append(el("p", { className: "message error", text: out.error }));
            return;
          }
          record(`Stopped ${jobName(body)}`, "", "cancelled");
          await afterStop(body.id);
        })
      );
      actions.append(stop);
    }
    if (watching === body.id) holder.replaceChildren(actions);
  }

  function closeJob() {
    watching = null;
    view = null;
    if (poller) window.clearTimeout(poller);
    $("job-detail-panel").hidden = true;
    if (window.location.hash.startsWith("#jobs/")) window.history.replaceState(null, "", "#jobs");
  }

  $("jobs-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshJobs));
  $("jobs-state").addEventListener("change", refreshJobs);
  $("job-detail-close").addEventListener("click", closeJob);
  $("activity-clear").addEventListener("click", (event) => {
    // Inside the disclosure's summary: clearing must not also open or close it.
    event.preventDefault();
    // Only this window's convenience list — see clearActivity in core.js.
    window.AUCOM.clearActivity();
    setMessage("jobs-message", "Cleared this window's list. The jobs below are untouched.", "");
  });

  window.AUCOM.areas.jobs = {
    // `#jobs/<id>` is one job's page; `#jobs` is the list.
    async refresh(argument) {
      const id = argument ? decodeURIComponent(argument) : "";
      await refreshJobs();
      if (id && id !== watching) await openJob(id);
      // Back to the list by its own address closes the open job; a refresh
      // asked for by another area (Run, after a start) leaves it alone.
      if (!id && watching && window.location.hash === "#jobs") closeJob();
    },
    open(id) {
      window.location.hash = jobHref(id);
    },
    href: jobHref,
  };
})();
