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
  const { $, el, api, setMessage, withBusy, record, badge, when, terminal, t } = window.AUCOM;

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
      job.started_at ? when(job.started_at) : "",
    ]
      .filter(Boolean)
      .join(" · ");

    const open = el("button", { text: "Open", attrs: { type: "button", class: "secondary", "data-control": "open" } });
    open.addEventListener("click", () => {
      window.location.hash = jobHref(job.id);
    });

    const actions = el("div", { className: "row-actions", children: [open] });
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
      view.root,
      el("p", {
        className: "muted",
        text: "Credentials the Companion knows about are removed from this view. The raw bytes are kept beside the job.",
      }),
      el("div", { attrs: { id: "job-detail-actions" } }),
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

  function renderSummary(body) {
    const key = keyOf(body);
    if (key === summaryKey) return;
    summaryKey = key;
    $("job-detail-title").textContent = jobName(body);
    const summary = $("job-detail-summary");
    const parts = [];

    const head = el("p");
    head.append(badge(body.state));
    head.append(document.createTextNode(" " + (body.started_at ? "started " + when(body.started_at) : "")));
    parts.push(head);

    if (body.command?.shell) {
      parts.push(el("h4", { text: "The command that ran" }));
      parts.push(el("pre", { className: "output", text: body.command.shell }));
    }
    // The user's own words in that command, named, so a job that behaved
    // differently because of a flag somebody added says which flag.
    if ((body.custom_args || []).length) {
      parts.push(el("p", {
        className: "custom-args-note",
        children: [
          el("strong", { text: t("Your own arguments:") + " " }),
          ...body.custom_args.map((token) => el("code", { className: "custom-arg", text: token })),
          el("span", { className: "muted", text: " " + t("(from this program's setup in Profiles)") }),
        ],
      }));
    }
    if (body.error) {
      parts.push(el("p", { className: "message error", text: body.error }));
    }
    if (body.failure_class) {
      parts.push(el("p", { className: "mono failure-class", text: t("Kind of failure: {kind}", { kind: body.failure_class }) }));
    }
    parts.push(
      el("p", {
        className: "mono",
        text: [
          body.exit_code !== undefined && body.exit_code !== null ? "exit status " + body.exit_code : "",
          body.timed_out ? "timed out" : "",
          body.started_at ? "started " + when(body.started_at) : "",
          body.finished_at ? "finished " + when(body.finished_at) : "",
        ]
          .filter(Boolean)
          .join(" · "),
      })
    );

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
          item.append(el("span", { className: "stage-detail", text: " " + artifact.path }));
        }
        list.append(item);
      }
      parts.push(list);
    }
    summary.replaceChildren(...parts);
    renderActions(body);
  }

  async function renderActions(body) {
    const holder = $("job-detail-actions");
    const actions = el("div", { className: "row-actions" });
    if (terminal(body.state)) {
      const retry = el("button", { text: "Run this again", attrs: { type: "button" } });
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
      actions.append(
        el("p", {
          className: "muted",
          text:
            "Running it again makes a NEW job with the same request. This one stays exactly as it is, " +
            "so both are in the list and neither is overwritten.",
        })
      );
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
