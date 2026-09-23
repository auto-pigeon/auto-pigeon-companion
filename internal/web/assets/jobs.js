// The Jobs area: everything that has run, with the command, the exit status and
// the output.
//
// Retry is spelled out rather than implied. A retried job is a NEW job with the
// same request, and the page says so — a button labelled "retry" that quietly
// re-ran something is how a build gets run twice by somebody who thought they
// were looking at the first one.

"use strict";

(() => {
  const { $, el, api, setMessage, withBusy, record, badge, when, terminal, t } = window.AUCOM;

  let watching = null;
  let poller = null;

  async function refreshJobs() {
    const list = $("jobs-list");
    const state = $("jobs-state").value;
    const query = new URLSearchParams({ limit: "40" });
    if (state) query.set("state", state);
    const { ok, body } = await api("/api/v1/jobs?" + query.toString());
    list.replaceChildren();
    if (!ok) {
      setMessage("jobs-message", body.error || "could not read the jobs", "error");
      return;
    }
    const jobs = body.items || [];
    setMessage("jobs-message", jobs.length === 0 ? "No jobs yet." : `${jobs.length} job(s).`);
    for (const job of jobs) list.append(jobRow(job));
  }

  function jobName(job) {
    const what = job.action_title || job.action_id || "a job";
    const who = job.profile_name || "";
    return job.label || (who ? `${what} · ${who}` : what);
  }

  function jobRow(job) {
    const head = el("div", { className: "row-head" });
    // Named by what ran, never by the job's id (operator, NEW_244D).
    head.append(el("strong", { text: jobName(job) }));
    head.append(badge(job.state));
    const detail = el("p", { className: "muted" });
    detail.textContent = [
      job.exit_code !== undefined && job.exit_code !== null ? "exit " + job.exit_code : "",
      job.started_at ? when(job.started_at) : "",
    ]
      .filter(Boolean)
      .join(" · ");

    const open = el("button", { text: "Open", attrs: { type: "button", class: "secondary" } });
    open.addEventListener("click", () => openJob(job.id));

    const actions = el("div", { className: "row-actions", children: [open] });
    if (!terminal(job.state)) {
      const cancel = el("button", { text: "Stop", attrs: { type: "button", class: "danger" } });
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
    return el("li", { className: "job-row", children: [el("div", { className: "job-row__text", children: [head, detail] }), actions] });
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
    if (watching === id) await renderJob();
  }

  async function openJob(id) {
    watching = id;
    const panel = $("job-detail-panel");
    const detail = $("job-detail");
    panel.hidden = false;
    detail.replaceChildren(el("p", { className: "muted", text: "Reading…" }));
    $("job-detail-title").setAttribute("tabindex", "-1");
    $("job-detail-title").focus();
    await renderJob();
  }

  async function renderJob() {
    if (!watching) return;
    const detail = $("job-detail");
    const { ok, body } = await api("/api/v1/jobs/" + encodeURIComponent(watching));
    if (!ok) {
      detail.replaceChildren(el("p", { className: "message error", text: body.error }));
      return;
    }
    $("job-detail-title").textContent = jobName(body);

    detail.replaceChildren();
    const head = el("p");
    head.append(badge(body.state));
    head.append(document.createTextNode(" " + (body.started_at ? "started " + when(body.started_at) : "")));
    detail.append(head);

    if (body.command?.shell) {
      detail.append(el("h4", { text: "The command that ran" }));
      detail.append(el("pre", { className: "output", text: body.command.shell }));
    }
    if (body.error) {
      detail.append(el("p", { className: "message error", text: body.error }));
    }
    detail.append(
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
      detail.append(el("h4", { text: "Findings" }));
      const list = el("ul");
      for (const diagnostic of body.diagnostics) {
        list.append(el("li", { children: [badge(diagnostic.severity, diagnostic.severity === "error" ? "failed" : "queued"), document.createTextNode(" " + diagnostic.message)] }));
      }
      detail.append(list);
    }

    if ((body.artifacts || []).length) {
      detail.append(el("h4", { text: "Artifacts" }));
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
      detail.append(list);
    }

    const logs = await api(`/api/v1/jobs/${encodeURIComponent(body.id)}/logs`);
    if (logs.ok) {
      detail.append(el("h4", { text: "Output" }));
      detail.append(el("pre", { className: "output", text: logs.body.text || "(nothing was printed)", attrs: { tabindex: "0" } }));
      detail.append(
        el("p", {
          className: "muted",
          text: "Credentials the Companion knows about are removed from this view. The raw bytes are kept beside the job.",
        })
      );
    }

    const actions = el("div", { className: "row-actions" });
    if (terminal(body.state)) {
      const retry = el("button", { text: "Run this again", attrs: { type: "button" } });
      retry.addEventListener("click", () =>
        withBusy(retry, async () => {
          const { ok, body: out } = await api(`/api/v1/jobs/${encodeURIComponent(body.id)}/retry`, { method: "POST" });
          if (!ok) {
            detail.append(el("p", { className: "message error", text: out.error }));
            return;
          }
          record(`Ran ${jobName(body)} again`, "", "running");
          await refreshJobs();
          await openJob(out.id);
        })
      );
      actions.append(retry);
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
            detail.append(el("p", { className: "message error", text: out.error }));
            return;
          }
          record(`Stopped ${jobName(body)}`, "", "cancelled");
          await afterStop(body.id);
        })
      );
      actions.append(stop);
    }
    detail.append(actions);

    if (poller) window.clearTimeout(poller);
    if (!terminal(body.state)) {
      poller = window.setTimeout(renderJob, 900);
    } else {
      await refreshJobs();
    }
  }

  $("jobs-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshJobs));
  $("jobs-state").addEventListener("change", refreshJobs);
  $("job-detail-close").addEventListener("click", () => {
    watching = null;
    if (poller) window.clearTimeout(poller);
    $("job-detail-panel").hidden = true;
  });
  $("activity-clear").addEventListener("click", (event) => {
    // Inside the disclosure's summary: clearing must not also open or close it.
    event.preventDefault();
    // Only this window's convenience list — see clearActivity in core.js.
    window.AUCOM.clearActivity();
    setMessage("jobs-message", "Cleared this window's list. The jobs below are untouched.", "");
  });

  window.AUCOM.areas.jobs = {
    refresh: refreshJobs,
    open: openJob,
  };
})();
