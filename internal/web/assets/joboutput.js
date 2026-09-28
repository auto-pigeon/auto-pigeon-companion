// The live output view: what a running job has printed, read a bounded chunk
// at a time from GET /api/v1/jobs/{id}/output (NEW_265).
//
// One implementation, used by the Jobs page and by the Activity drawer, so the
// two show the same bytes from the same source with the same cursor rules.
//
// Four rules this file keeps, each from something that went wrong or was asked
// for:
//
//   - text only. Tool output is appended to a text node with appendData; it is
//     never parsed as markup, so a compiler line that happens to look like a tag
//     is a line, not an element.
//   - the DOM is appended to, never rebuilt. Replacing a log's element once a
//     second threw away the reader's scroll position, their selection and their
//     keyboard focus; appending to the same text node keeps all three.
//   - bounded. The server hands out at most one chunk per request and this view
//     keeps at most `maxChars` characters; the oldest are trimmed, and the view
//     says so and where the whole log is.
//   - an error is shown beside the output, never instead of it. A poll that
//     failed does not erase what was already read.

"use strict";

(() => {
  const { el, api, t } = window.AUCOM;

  // Bytes asked for per request, and how many catch-up requests one poll may
  // make when the reader is behind (a job opened half-way through a flood).
  const CHUNK = 64 * 1024;
  const CATCH_UP = 4;

  function outputView({ label = "Output", maxChars = 200000, sourcePicker = false, jobLink = null } = {}) {
    const state = {
      job: "",
      source: "", // the source id the cursor belongs to
      cursor: null, // null: start at the end of what exists
      pinned: "", // a source the person chose, or "" for automatic
      complete: false,
      stderrShown: false,
      chars: 0,
      trimmed: false,
      busy: false,
    };

    const status = el("span", { className: "joblog__source muted" });
    const head = el("div", { className: "joblog__head", children: [el("strong", { text: label }), status] });
    const picker = el("select", { className: "joblog__picker", attrs: { "aria-label": t("Which output to show") } });
    if (sourcePicker) {
      picker.append(el("option", { text: t("Automatic"), attrs: { value: "" } }));
      head.append(picker);
      picker.addEventListener("change", () => {
        state.pinned = picker.value;
        restart(t("Showing {what}.", { what: picker.selectedOptions[0]?.textContent || "" }));
        poll();
      });
    }
    if (jobLink) head.append(jobLink);

    const trimmedNote = el("p", {
      className: "joblog__trimmed muted small",
      text: t("Earlier lines are trimmed from this view to keep it small. The whole log is kept with the job."),
    });
    trimmedNote.hidden = true;
    const scroller = el("div", {
      className: "joblog__scroll",
      attrs: { role: "log", "aria-live": "off", tabindex: "0", "aria-label": label },
    });
    const error = el("p", { className: "message error joblog__error", attrs: { role: "status" } });
    error.hidden = true;
    const root = el("div", { className: "joblog", children: [head, trimmedNote, scroller, error] });

    let block = null; // the block text is being appended to

    function newBlock(title) {
      const text = document.createTextNode("");
      const pre = el("pre", { className: "joblog__text" });
      pre.append(text);
      const children = [];
      if (title) children.push(el("p", { className: "joblog__stage", text: title }));
      children.push(pre);
      const node = el("section", { className: "joblog__block", children });
      scroller.append(node);
      block = { node, text };
      return block;
    }

    function atBottom() {
      return scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 8;
    }

    // append adds text to the current block and keeps the reader where they
    // were: following the end if they were at it, still if they had scrolled
    // up to read something.
    function append(text) {
      if (!text) return;
      if (!block) newBlock("");
      const follow = atBottom();
      block.text.appendData(text);
      state.chars += text.length;
      trim();
      if (follow) scroller.scrollTop = scroller.scrollHeight;
    }

    // trim removes the oldest characters once the view holds more than its
    // bound, keeping the visible position steady while it does.
    function trim() {
      if (state.chars <= maxChars) return;
      const before = scroller.scrollHeight;
      let excess = state.chars - maxChars;
      for (const node of [...scroller.querySelectorAll(".joblog__block")]) {
        if (excess <= 0) break;
        const text = node.querySelector("pre").firstChild;
        const length = text?.length || 0;
        if (length <= excess && node !== block?.node) {
          node.remove();
          excess -= length;
          state.chars -= length;
          continue;
        }
        const cut = Math.min(excess, length);
        text.deleteData(0, cut);
        excess -= cut;
        state.chars -= cut;
      }
      state.trimmed = true;
      trimmedNote.hidden = false;
      scroller.scrollTop -= before - scroller.scrollHeight;
    }

    function note(text) {
      append((block && block.text.length && !block.text.data.endsWith("\n") ? "\n" : "") + "[" + text + "]\n");
    }

    function restart(why) {
      state.source = "";
      state.cursor = null;
      state.complete = false;
      state.stderrShown = false;
      if (block) block.text.data = "";
      state.chars = [...scroller.querySelectorAll("pre")].reduce((n, pre) => n + (pre.firstChild?.length || 0), 0);
      if (why) note(why);
    }

    function setError(text) {
      error.textContent = text || "";
      error.hidden = !text;
    }

    function describe(body) {
      const source = body.source || {};
      let text = source.label || "";
      if (source.kind === "sidecar" && !source.exists) {
        text = t("waiting for {what} — the program creates it when it gets there", { what: source.label });
      } else if (body.complete && state.chars === 0) {
        text = t("{what} — nothing was printed", { what: source.label });
      } else if (!body.terminal) {
        text = t("{what} — live", { what: source.label });
      }
      status.textContent = text ? " · " + text : "";
      if (sourcePicker) {
        const want = ["", ...(body.sources || []).map((s) => s.id)];
        const have = [...picker.options].map((o) => o.value);
        if (want.join("|") !== have.join("|")) {
          const chosen = picker.value;
          picker.replaceChildren(el("option", { text: t("Automatic"), attrs: { value: "" } }));
          for (const s of body.sources || []) {
            picker.append(el("option", { text: s.label, attrs: { value: s.id } }));
          }
          picker.value = want.includes(chosen) ? chosen : "";
        }
      }
    }

    async function read(source, from, offset) {
      const query = new URLSearchParams({ source: source || "auto", max: String(CHUNK) });
      if (from) query.set("from", from);
      if (offset !== null && offset !== undefined) query.set("offset", String(offset));
      return api(`/api/v1/jobs/${encodeURIComponent(state.job)}/output?` + query.toString());
    }

    // poll reads until it has caught up, or for a bounded number of chunks,
    // and reports whether the job has stopped and been read to the end.
    async function poll() {
      if (!state.job || state.busy) return { terminal: state.complete, complete: state.complete };
      state.busy = true;
      const job = state.job;
      try {
        for (let round = 0; round < CATCH_UP; round += 1) {
          const { ok, body } = await read(state.pinned, state.source, state.cursor);
          if (job !== state.job) return { terminal: false, complete: false };
          if (!ok) {
            setError(t("The output could not be read just now: {why}. What was already read is kept above.", {
              why: body.error || "no answer",
            }));
            return { terminal: false, complete: false, error: true };
          }
          setError("");
          if (state.source && body.source.id !== state.source) {
            // The program started filling a better source — its own log file.
            // That file holds the whole transcript, so it replaces what the
            // other stream had shown rather than following it.
            if (block) block.text.data = "";
            state.chars = 0;
            note(t("now showing {what}", { what: body.source.label }));
          } else if (body.reset && state.source) {
            if (block) block.text.data = "";
            state.chars = 0;
            note(t("{what} was rewritten; showing it again from the start", { what: body.source.label }));
          }
          state.source = body.source.id;
          if (body.gap > 0) {
            note(t("{n} bytes of output are not shown here", { n: body.gap }));
          }
          append(body.text);
          state.cursor = body.next;
          describe(body);
          if (body.complete) {
            state.complete = true;
            await appendStderr(body);
            return { terminal: true, complete: true };
          }
          if (!(body.next < body.size)) return { terminal: body.terminal, complete: false };
        }
        return { terminal: false, complete: false };
      } finally {
        state.busy = false;
      }
    }

    // A failing program often says why on stderr while its log says what it
    // was doing. Once it has stopped, stderr is added below the log, labelled,
    // when it holds anything the view has not shown.
    async function appendStderr(body) {
      if (state.stderrShown || state.pinned) return;
      state.stderrShown = true;
      const stderr = (body.sources || []).find((s) => s.id === "stderr");
      if (!stderr || !stderr.bytes || body.source.id === "stderr") return;
      let cursor = null;
      let first = true;
      for (let round = 0; round < CATCH_UP; round += 1) {
        const { ok, body: out } = await read("stderr", "stderr", cursor);
        if (!ok) return;
        if (first && out.text) {
          newBlock(out.source.label);
          first = false;
        }
        append(out.text);
        cursor = out.next;
        if (out.complete || !(out.next < out.size)) return;
      }
    }

    return {
      root,
      // follow switches the view to a job. A different job starts a new
      // block, titled when the caller names the stage, and keeps what the
      // earlier stages printed above it. `fromStart` reads the job from its
      // first byte — a stage being watched as it runs; otherwise the view
      // opens on the job's last chunk.
      follow(jobID, title = "", { fromStart = false } = {}) {
        if (!jobID || jobID === state.job) return false;
        state.job = jobID;
        state.source = "";
        state.cursor = fromStart ? 0 : null;
        state.complete = false;
        state.stderrShown = false;
        newBlock(title);
        return true;
      },
      poll,
      get job() {
        return state.job;
      },
      get complete() {
        return state.complete;
      },
      setError,
    };
  }

  window.AUCOM.outputView = outputView;
})();
