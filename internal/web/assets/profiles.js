// The Profiles area: reading a profile, writing one, and approving one.
//
// The wizard never builds JSON. It posts the fields somebody filled in and the
// Companion composes, validates and digests the document — so what the advanced
// view shows is the real document rather than a prediction of one, and the
// browser is not a second implementation of a schema it would eventually
// disagree with.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, record, badge, when,
    maturityBadge, maturityNote, openCompatibilityReport, t } = window.AUCOM;

  let installed = [];
  let templates = [];
  let composed = null;
  let step = 1;

  // --- installed profiles ---------------------------------------------------

  async function refreshList() {
    const list = $("profiles-list");
    const kind = $("profiles-kind").value;
    const { ok, body } = await api("/api/v1/profiles" + (kind ? "?kind=" + encodeURIComponent(kind) : ""));
    list.replaceChildren();
    if (!ok) {
      setMessage("profiles-message", body.error || "could not read the profiles", "error");
      return;
    }
    installed = body.items || [];
    setMessage("profiles-message", t("{n} profile(s) on this machine.", { n: installed.length }));
    for (const profile of installed) list.append(profileCard(profile));
  }

  // Where a profile's programs live, said from its own file names: "the one
  // containing bin" when they share a subfolder, or the programs themselves
  // when they sit at the top. It used to say "For ericw-tools…" whatever the
  // profile was.
  function folderHint(executables) {
    const files = executables.map((executable) => window.AUCOM.programFileName(executable.file)).filter(Boolean);
    if (!files.length) return "";
    const tops = [...new Set(files.map((file) => (file.includes("/") ? file.split("/")[0] : "")))];
    if (tops.length === 1 && tops[0]) {
      return t("The folder you unpacked the programs into — the one containing {folder}.", { folder: tops[0] });
    }
    const names = files.map((file) => file.split("/").pop());
    return t("The folder that has {programs} directly inside it.", { programs: names.slice(0, 4).join(", ") + (names.length > 4 ? ", …" : "") });
  }

  function profileCard(profile) {
    // Fixed rows, so the text lines up across a row of cards (operator,
    // 2026-09-22): two lines for the name, two for the tags, four for the
    // summary. Anything longer is cut with an ellipsis, and the whole text is
    // in the tooltip.
    const title = el("h4", { className: "profile-card__title", text: profile.name, attrs: { title: profile.name } });
    const marks = el("p", { className: "profile-card__tags" });
    marks.append(badge(profile.trust));
    marks.append(document.createTextNode(" "));
    marks.append(badge(profile.kind, "queued"));
    marks.append(document.createTextNode(" "));
    marks.append(badge(profile.authorized ? "approved" : "not approved", profile.authorized ? "ok" : "failed"));
    // Whether its programs can start here: the same decision Build & Run's
    // engine list shows (NEW_265), so the two never disagree.
    if (profile.readiness) {
      marks.append(document.createTextNode(" "));
      marks.append(badge(profile.readiness.ready ? t("ready") : t("needs setup"), profile.readiness.ready ? "ok" : "blocked"));
    }
    const wip = maturityBadge(profile.maturity);
    if (wip) {
      marks.append(document.createTextNode(" "));
      marks.append(wip);
    }

    const summary = el("p", {
      className: "muted profile-card__summary",
      text: profile.summary || "",
      attrs: { title: profile.summary || null },
    });
    const identity = el("p", { className: "muted profile-card__version", text: t("Version {version}", { version: profile.version }) });

    // Configuring a profile and reviewing one are different user tasks, so they
    // are different words (246I defect 2). An installed profile whose document
    // has already been approved is a thing you CONFIGURE; "Review" is the
    // security decision, and it stays on the card only while that decision is
    // genuinely still outstanding. Nothing about the digest or grant checks
    // changes here — only which of the two tasks the card offers.
    const needsReview = !profile.authorized;
    const open = el("button", {
      text: needsReview ? "Review" : "Configure",
      attrs: { type: "button" },
    });
    // Either task is the profile's own page, a step below Profiles.
    open.addEventListener("click", () => configure(profile.id));

    // Homepage opens another site, so it sits top right (operator,
    // 2026-09-23); only for the programs a person installs — engines and
    // build tools — and only when the document names one.
    const head = el("div", { className: "profile-card__head", children: [title] });
    if (profile.homepage && (profile.kind === "engine" || profile.kind === "tool")) {
      head.append(el("a", {
        className: "profile-card__homepage",
        text: t("Homepage"),
        attrs: { href: profile.homepage, target: "_blank", rel: "noopener noreferrer", title: profile.homepage },
      }));
    }

    return el("li", {
      className: "card profile-card",
      children: [head, marks, summary, identity, el("div", { className: "row-actions", children: [open] })],
    });
  }

  // homepageSection is where a profile's homepage is read and — for a profile
  // of this machine's own — changed (operator, 2026-09-23). A built-in document
  // is part of the program, so its answer is a copy of one's own, made in New
  // profile from it. An edit is a new document version with a new digest, so
  // it has to be approved again; the page says so before and after.
  function homepageSection(body) {
    const section = el("div", { className: "profile-homepage" });
    section.append(el("h4", { text: t("Homepage") }));
    if (!body.editable) {
      const line = el("p", { className: "muted" });
      if (body.homepage) {
        line.append(el("a", { text: body.homepage, attrs: { href: body.homepage, target: "_blank", rel: "noopener noreferrer" } }));
        line.append(document.createTextNode(" "));
      } else {
        line.append(document.createTextNode(t("This profile names no homepage.") + " "));
      }
      line.append(document.createTextNode(t("It shipped with the Companion, so it cannot be edited here; make your own profile from it to set a different homepage.")));
      section.append(line);
      const copy = el("button", { text: t("Make your own copy"), attrs: { type: "button", class: "secondary" } });
      copy.addEventListener("click", () => {
        window.open(window.location.pathname + "?view=new-profile#new-profile/" + encodeURIComponent(body.id), "_blank", "noopener");
      });
      section.append(el("div", { className: "row-actions", children: [copy] }));
      return section;
    }
    const input = el("input", {
      attrs: { type: "url", id: "profile-homepage", spellcheck: "false", "aria-label": t("Homepage") },
    });
    input.value = body.homepage || "";
    const status = el("p", { className: "message", attrs: { role: "status" } });
    const save = el("button", { text: t("Save homepage"), attrs: { type: "button", class: "primary" } });
    const refreshSave = () => { save.disabled = input.value.trim() === (body.homepage || ""); };
    input.addEventListener("input", refreshSave);
    refreshSave();
    save.addEventListener("click", () =>
      withBusy(save, async () => {
        const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(body.id)}/homepage`, {
          method: "POST", body: { homepage: input.value.trim() },
        });
        if (!ok) {
          setMessage(status, out.error, "error");
          return;
        }
        record(`Homepage of ${body.name} saved (version ${out.was_version} → ${out.version})`, input.value.trim(), "ok");
        await openProfile(body.id);
        await refreshList();
      })
    );
    section.append(
      el("p", { className: "muted small", text: t("Saving makes a new version of this profile, which has to be approved again before it runs.") }),
      el("div", { className: "field", children: [input] }),
      el("div", { className: "row-actions", children: [save] }),
      status,
    );
    return section;
  }

  // configure opens one profile's configuration page IN this window, under
  // Profiles: `#profiles/<id>`, a hash that survives a reload and names the
  // profile and nothing else. It used to open a new tab without the area
  // navigation; the operator asked for the side panel to stay and for the page
  // to read as a step below Profiles (2026-09-23), so it is a sub-page now —
  // Profiles › Configure › <name> — and the installed list is not drawn above it.
  function configure(id) {
    window.AUCOM.showArea("profiles/" + encodeURIComponent(id));
  }

  // showOverview switches the area between the list and one profile's page.
  function showOverview(overview) {
    $("profiles-overview").hidden = !overview;
    $("profile-breadcrumb").hidden = overview;
    $("profile-detail-panel").hidden = overview;
  }

  async function openProfile(id) {
    const panel = $("profile-detail-panel");
    const detail = $("profile-detail");
    panel.hidden = false;
    detail.replaceChildren(el("p", { className: "muted", text: "Reading…" }));

    const { ok, body } = await api("/api/v1/profiles/" + encodeURIComponent(id));
    detail.replaceChildren();
    if (!ok) {
      detail.append(el("p", { className: "message error", text: body.error }));
      return;
    }
    $("profile-detail-title").textContent = body.name;
    $("profile-breadcrumb-name").textContent = body.name;
    $("profile-detail-title").setAttribute("tabindex", "-1");
    $("profile-detail-title").focus();

    const marks = el("p");
    marks.append(badge(body.trust));
    marks.append(document.createTextNode(" " + (body.trust_description || "")));
    detail.append(marks);
    // Above the summary and above the permission review: a warning that arrives
    // after the thing it is about is a warning read after the decision.
    const note = maturityNote(body.maturity, () =>
      openCompatibilityReport({
        family: body.engine_family,
        about: `About ${body.name}.`,
        profiles: [{ role: body.kind === "engine" ? "engine" : body.kind, id: body.id, version: body.version }],
      })
    );
    if (note) detail.append(note);
    detail.append(el("p", { text: body.summary || "" }));
    if (body.description) detail.append(el("p", { className: "muted", text: body.description }));
    detail.append(
      el("p", {
        className: "muted",
        // The terminal's permission report is not drawn here: it names the id,
        // and every line of it is already on this panel in a person's words.
        text: `Version ${body.version}` + (body.publisher?.name ? ` · published by ${body.publisher.name}` : "") +
          (body.license?.spdx ? ` · ${body.license.spdx}` : ""),
      })
    );

    detail.append(el("h4", { text: "Actions" }));
    const actions = el("ul");
    for (const action of body.actions || []) {
      actions.append(
        el("li", {
          text: action.title || action.id,
        })
      );
    }
    detail.append(actions);
    if (body.kind === "engine" || body.kind === "tool") detail.append(homepageSection(body));

    // The approval. It is the whole reason this panel exists, and it is
    // deliberately not something the page can do on somebody's behalf: the
    // digest goes back with it, and the server refuses one that is not the
    // document on disk.
    const status = el("p", { className: "message", attrs: { role: "status" } });
    const buttons = el("div", { className: "row-actions" });
    // `authorized` is profile.Authorize's own answer, not a guess assembled
    // from trust and grant. Only a built-in document runs without one: a
    // signature says who published something, never that you agreed to it.
    if (body.authorized && body.trust === "builtin") {
      status.textContent =
        "This document arrived inside the Companion, so installing the program was the decision. " +
        "There is nothing else to approve.";
    } else if (body.authorized) {
      status.textContent = t("Approved on {when}.", { when: when(body.binding?.granted_at) });
      status.className = "message ok";
      const withdraw = el("button", { text: "Withdraw approval", attrs: { type: "button", class: "danger" } });
      withdraw.addEventListener("click", () =>
        withBusy(withdraw, async () => {
          const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(id)}/withdraw`, { method: "POST" });
          if (!ok) {
            setMessage(status, out.error, "error");
            return;
          }
          record(`Withdrew approval for ${body.name || "the profile"}`, "", "cancelled");
          await refreshList();
          await openProfile(id);
        })
      );
      buttons.append(withdraw);
    } else {
      // One sentence: what it asks for is listed just above, and the raw
      // refusal repeated that list word for word.
      status.textContent = t("Not approved on this machine yet. Read what it asks for above, then approve it.");
      status.className = "message error";
      const approve = el("button", { text: "I have read this — approve it", attrs: { type: "button", class: "primary" } });
      approve.addEventListener("click", () =>
        withBusy(approve, async () => {
          const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(id)}/grant`, {
            method: "POST",
            body: { digest: body.digest },
          });
          if (!ok) {
            setMessage(status, out.error, "error");
            return;
          }
          record(`Approved ${body.name || "the profile"}`, "", "ok");
          await refreshList();
          await openProfile(id);
        })
      );
      buttons.append(approve);
    }


    if (body.editable) {
      const remove = el("button", { text: "Remove this profile", attrs: { type: "button", class: "danger" } });
      remove.addEventListener("click", () =>
        withBusy(remove, async () => {
          const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(id)}/remove`, { method: "POST" });
          if (!ok) {
            setMessage(status, out.error, "error");
            return;
          }
          record(`Removed the profile ${id}`, out.was, "cancelled");
          $("profile-detail-panel").hidden = true;
          await refreshList();
        })
      );
      buttons.append(remove);
    }

    detail.append(buttons);
    detail.append(status);

    renderReadiness(detail, body);
    renderSetup(detail, body);
    renderArguments(detail, body);
  }

  // renderReadiness says whether this profile's programs can start here and,
  // when they cannot, exactly what is missing — the answer Build & Run's engine
  // list gives, from the same server-side decision.
  function renderReadiness(detail, body) {
    if (!body.readiness) return;
    const box = el("div", { className: "readiness", attrs: { id: "profile-readiness" } });
    if (body.readiness.ready) {
      box.append(el("p", { className: "message ok", text: t("Ready: everything its programs need is on this machine.") }));
    } else {
      box.append(el("p", { className: "message error", text: t("Not ready to start yet:") }));
      box.append(el("ul", {
        className: "plain",
        children: (body.readiness.problems || []).map((problem) => el("li", { text: problem.summary })),
      }));
    }
    detail.append(box);
  }

  // --- your own arguments -----------------------------------------------------
  //
  // Parameters belong to a PIPELINE's stages (operator, 2026-10-03): "in build
  // tools you set up the paths and metadata of tools, in pipelines you pick a
  // tool and add the parameters". So:
  //
  //   - a pipeline shows every stage with the tool that runs it here and one
  //     list of argv tokens for that stage alone — the same tool twice in one
  //     pipeline gets two lists;
  //   - a build tool shows its commands and takes no tokens. Ones recorded on
  //     it before this still apply, are shown, and can only be removed;
  //   - an engine is not a stage of anything and keeps its own (NEW_265).
  //
  // Each box is one argument exactly as typed. Nothing is split, quoted or
  // passed through a shell, which is why a Windows path with spaces is one box.
  // The profile document is not changed; the tokens live with this machine's
  // setup and survive a restart, a re-import and an update of the profile.
  function renderArguments(detail, body) {
    if (body.kind === "pipeline") { renderStageArguments(detail, body); return; }
    const executables = executableNames(body);
    if (executables.length === 0) return;
    const tool = body.kind === "tool";
    const saved = body.binding?.arguments || {};
    const section = el("div", { className: "profile-arguments", attrs: { id: "profile-arguments" } });
    const previews = el("div", { className: "profile-arguments__previews" });
    if (tool) {
      section.append(el("h4", { text: t("Parameters are set in pipelines") }));
      section.append(el("p", {
        className: "muted",
        text: t("A build tool says where its programs are and what they can do. What each one is run with is set on the stage of the pipeline that runs it: open a pipeline in Profiles and add the arguments to its stage, or write a pipeline of your own with New profile."),
      }));
      const older = executables.filter((executable) => (saved[executable.name] || []).length > 0);
      if (older.length > 0) {
        section.append(el("p", {
          className: "message",
          text: t("Arguments recorded on this tool earlier still reach every pipeline that runs it. They can be removed here; new ones go on a pipeline's stage."),
        }));
        for (const executable of older) {
          section.append(argumentsEditor({
            profile: body, legend: `${executable.title || executable.name} (${executable.name})`, name: executable.name,
            tokens: saved[executable.name], route: "arguments", key: "executable",
            read: (out) => out.binding?.arguments?.[executable.name] || [], removeOnly: true, previews,
          }));
        }
      }
    } else {
      section.append(el("h4", { text: t("Your own arguments") }));
      section.append(el("p", {
        className: "muted",
        text: t("Extra words added to every command that runs one program, before its input files — for a flag this profile does not offer as an option. Each box is one argument exactly as you type it; nothing splits or quotes it. Only that program gets them."),
      }));
      for (const executable of executables) {
        section.append(argumentsEditor({
          profile: body, legend: `${executable.title || executable.name} (${executable.name})`, name: executable.name,
          tokens: saved[executable.name] || [], route: "arguments", key: "executable",
          read: (out) => out.binding?.arguments?.[executable.name] || [], previews,
        }));
      }
    }
    section.append(el("h5", { text: t("The commands, as they will run") }));
    section.append(el("p", {
      className: "muted small",
      text: tool
        ? t("Each program as this tool declares it. A pipeline's stage adds its own arguments before the input files. Parts in angle brackets are filled in when a job runs — its folder, its input files, the map.")
        : t("Your own arguments are highlighted. Parts in angle brackets are filled in when a job runs — its folder, its input files, the map."),
    }));
    section.append(previews);
    detail.append(section);
    refreshPreviews(body.id, previews);
  }

  // A pipeline's stages: which tool runs each one here, and that stage's own
  // arguments.
  function renderStageArguments(detail, body) {
    const stages = body.stages || [];
    if (stages.length === 0) return;
    const section = el("div", { className: "profile-arguments", attrs: { id: "profile-arguments" } });
    section.append(el("h4", { text: t("Stages and their parameters") }));
    section.append(el("p", {
      className: "muted",
      text: t("Each stage is run by one tool. Add arguments for a stage here — a flag the tool does not offer as an option. Each box is one argument exactly as you type it; nothing splits or quotes it. They belong to this stage of this pipeline only: another pipeline using the same tool does not get them, and the same tool twice in one pipeline gets two lists. The Build area's last step shows the exact command before anything runs."),
    }));
    for (const stage of stages) {
      const tool = stage.tool;
      const legend = `${stage.title || stage.id} (${stage.id})`;
      const notes = [el("p", {
        className: "muted small",
        text: tool
          ? t("Run by {tool}: {action} — the program {program}.", { tool: tool.profile_name, action: tool.action_title || tool.action_id, program: tool.executable })
          : t("No installed tool provides {capability} yet, so this stage cannot run here. Its arguments can still be set.", { capability: stage.capability }),
      })];
      const set = Object.entries(stage.options || {});
      if (set.length > 0) {
        notes.push(el("p", { className: "muted small", text: t("The pipeline itself sets: {options}.", { options: set.map(([name, value]) => `${name} = ${value}`).join(", ") }) }));
      }
      if ((stage.tool_arguments || []).length > 0) {
        notes.push(el("p", {
          className: "message",
          text: t("{tool} also has arguments of its own recorded earlier, which reach this stage first: {tokens}. Remove them on the tool's page to keep everything here.", { tool: tool?.profile_name || "", tokens: stage.tool_arguments.join(" ") }),
        }));
      }
      section.append(argumentsEditor({
        profile: body, legend, name: stage.id, tokens: stage.arguments || [], route: "stage-arguments", key: "stage",
        read: (out) => (out.stages || []).find((item) => item.id === stage.id)?.arguments || [], notes, stage: true,
      }));
    }
    detail.append(section);
  }

  // One list of argument tokens with Add, Save and Reset. `route` and `key`
  // say whose they are (a program of a profile, or a stage of a pipeline);
  // `removeOnly` is a build tool's older tokens, which can only be taken away.
  function argumentsEditor({ profile: body, legend, name, tokens, route, key, read, previews, notes = [], removeOnly = false, stage = false }) {
    const id = (stage ? "profile-stage-args-" : "profile-args-") + name;
    const fieldset = el("fieldset", { className: "args-editor", attrs: { id } });
    fieldset.append(el("legend", { text: legend }));
    for (const note of notes) fieldset.append(note);
    const list = el("ol", { className: "args-editor__list", attrs: { "aria-label": t("Arguments for {program}", { program: name }) } });
    const status = el("p", { className: "message", attrs: { role: "status" } });

    const addRow = (value = "") => {
      const input = el("input", {
        attrs: { type: "text", spellcheck: "false", autocomplete: "off", "aria-label": t("Argument"), "data-args-for": name },
      });
      input.value = value;
      if (removeOnly) input.readOnly = true;
      const remove = el("button", { text: t("Remove"), attrs: { type: "button", class: "secondary" } });
      const row = el("li", { className: "args-editor__row", children: removeOnly ? [input] : [input, remove] });
      remove.addEventListener("click", () => {
        const next = row.nextElementSibling?.querySelector("input") || row.previousElementSibling?.querySelector("input");
        row.remove();
        (next || add).focus();
      });
      list.append(row);
      return input;
    };
    for (const token of tokens) addRow(token);

    const add = el("button", { text: t("Add argument"), attrs: { type: "button", class: "secondary" } });
    add.addEventListener("click", () => addRow("").focus());
    const save = el("button", { text: t("Save arguments"), attrs: { type: "button", class: "primary" } });
    const reset = el("button", { text: removeOnly ? t("Remove these arguments") : t("Reset to default"), attrs: { type: "button" } });

    const send = (payload, done) =>
      withBusy(removeOnly ? reset : save, async () => {
        const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(body.id)}/${route}`, {
          method: "POST", body: { [key]: name, ...payload },
        });
        if (!ok) {
          setMessage(status, out.error || t("The arguments could not be saved."), "error");
          return;
        }
        const now = read(out);
        list.replaceChildren();
        for (const token of now) addRow(token);
        setMessage(status, done(now), "ok");
        record(`${name}: ${now.length ? now.join(" ") : "no arguments of your own"}`, body.name, "ok");
        if (previews) renderPreviewItems(previews, out.commands || []);
      });

    save.addEventListener("click", () => {
      const values = [...list.querySelectorAll("input")].map((input) => input.value);
      send({ arguments: values }, (now) => now.length
        ? t("Saved. {program} now gets {n} argument(s) of your own; it survives a restart.", { program: name, n: now.length })
        : t("Saved. {program} gets only the profile's own arguments.", { program: name }));
    });
    reset.addEventListener("click", () => send({ reset: true }, () =>
      t("Reset. {program} runs with the profile's own arguments only.", { program: name })));

    fieldset.append(list, el("div", { className: "row-actions", children: removeOnly ? [reset] : [add, reset, save] }), status);
    return fieldset;
  }

  async function refreshPreviews(id, previews) {
    const { ok, body } = await api(`/api/v1/profiles/${encodeURIComponent(id)}/commands`);
    if (!ok) {
      previews.replaceChildren(el("p", { className: "message error", text: body.error || "" }));
      return;
    }
    renderPreviewItems(previews, body.items || []);
  }

  // The command as it would be typed on THIS machine, for reading only; the
  // program is given the words one by one, never through a shell.
  function quoteWord(word) {
    const text = String(word);
    if (/^windows\//.test(window.AUCOM.status?.platform || "")) {
      if (text && !/[\s"&|<>^%]/.test(text)) return text;
      return '"' + text.replace(/"/g, '""') + '"';
    }
    if (text && /^[A-Za-z0-9_@%+=:,./-]+$/.test(text)) return text;
    return "'" + text.replace(/'/g, "'\\''") + "'";
  }

  function renderPreviewItems(previews, items) {
    previews.replaceChildren();
    for (const item of items) {
      const block = el("div", { className: "args-preview", attrs: { "data-action": item.action_id } });
      block.append(el("p", { className: "args-preview__title", text: `${item.title || item.action_id} — ${item.executable}` }));
      if (item.error) {
        block.append(el("p", { className: "muted small", text: item.error }));
        previews.append(block);
        continue;
      }
      const code = el("code", { className: "args-preview__argv" });
      const from = item.custom_at || -1;
      const count = (item.custom_args || []).length;
      (item.argv || []).forEach((word, index) => {
        if (index > 0) code.append(document.createTextNode(" "));
        const own = count > 0 && index >= from && index < from + count;
        code.append(own ? el("mark", { className: "custom-arg-token", text: quoteWord(word) }) : document.createTextNode(quoteWord(word)));
      });
      block.append(el("pre", { className: "args-preview__code", children: [code] }));
      previews.append(block);
    }
  }

  // renderSetup is the local half: where the programs this profile declares
  // actually are on this machine.
  //
  // It is here and not only in the Run area because a hand-installed compiler
  // needs exactly what a hand-installed engine needs — somebody to say where it
  // is. Without it the Build area has a tool it can see, describe and approve,
  // and cannot start.
  function renderSetup(detail, body) {
    const executables = executableNames(body);
    if (executables.length === 0) return;

    detail.append(el("h4", { text: "Where these programs are on this machine", attrs: { id: "profile-setup" } }));
    detail.append(
      el("p", {
        className: "muted",
        text:
          "This is recorded separately from the profile and is never part of a profile you export. " +
          "Choose the folder you unpacked the programs into, or name each program below.",
      })
    );

    // Provenance, in words. A program somebody named is not a verified download,
    // and the page must not let the builtin badge above read as if it were
    // (NEW_244D).
    const provenance = el("p", { className: "provenance", attrs: { id: "profile-provenance" } });
    const describeProvenance = (binding) => {
      const acquisition = binding?.acquisition || "";
      const named = Object.values(binding?.executables || {}).filter(Boolean).length;
      provenance.replaceChildren();
      if (named === 0) {
        provenance.append(badge("not set up", "blocked"),
          document.createTextNode(" Nothing is recorded yet, so a build that needs these programs cannot start."));
      } else {
        provenance.append(badge("local binding", "local"),
          document.createTextNode(" " + t("Programs you named on this machine. The Companion runs what you pointed at and downloads nothing.") +
            (body.trust === "builtin" ? " " + t("The profile's builtin badge is about the document, not about these bytes.") : "")));
      }
    };
    describeProvenance(body.binding);
    detail.append(provenance);

    // The Companion downloads no program (operator, 2026-09-23): the way to a
    // program nobody has installed yet is its own homepage.
    if (body.homepage) {
      const get = el("p", { className: "muted" });
      get.append(document.createTextNode(t("Don't have it yet? Get it from its homepage, install or unpack it, then choose its folder below:") + " "));
      get.append(el("a", { text: body.homepage, attrs: { href: body.homepage, target: "_blank", rel: "noopener noreferrer" } }));
      detail.append(get);
    }

    const folder = window.AUCOM.pathField({
      id: "profile-folder",
      kind: "directory",
      label: t("The folder that holds {name}", { name: body.name }),
      hint: folderHint(body.executables || []),
    });
    detail.append(folder.container);
    const useFolder = el("button", { text: "Use this folder", attrs: { type: "button", class: "primary", id: "profile-use-folder" } });
    const folderStatus = el("p", { className: "message", attrs: { role: "status" } });
    detail.append(el("div", { className: "row-actions", children: [useFolder] }), folderStatus);

    const fields = new Map();
    for (const executable of executables) {
      const field = window.AUCOM.pathField({
        id: "profile-exe-" + executable.name,
        kind: "open-file",
        label: `${executable.title || executable.name}`,
        value: (body.binding?.executables || {})[executable.name] || "",
        hint: t("The profile looks for a file named {file}.", { file: window.AUCOM.programFileName(executable.file) }),
      });
      fields.set(executable.name, field);
      detail.append(field.container);
    }
    // Folders the actions use besides their job folder — a texture folder, a
    // game directory. The document names the role; this machine says where.
    const roots = new Map();
    for (const action of body.actions || []) {
      for (const root of action.roots || []) {
        if (root.role === "workspace") continue;
        const purposes = roots.get(root.role) || [];
        if (root.purpose && !purposes.includes(root.purpose)) purposes.push(root.purpose);
        purposes.optional = purposes.optional === undefined ? Boolean(root.optional) : purposes.optional && Boolean(root.optional);
        roots.set(root.role, purposes);
      }
    }
    const rootFields = new Map();
    for (const [role, purposes] of roots) {
      const field = window.AUCOM.pathField({
        id: "profile-root-" + role,
        kind: "directory",
        label: `${window.AUCOM.folderTitle(role)}${purposes.optional ? " (optional)" : ""}`,
        value: (body.binding?.roots || {})[role] || "",
        hint: purposes.length ? `Used to ${purposes.join("; ")}.` : "",
      });
      rootFields.set(role, field);
      detail.append(field.container);
    }

    // "Look for installed games" fills the game folder in from the usual
    // places — moved here from Run, because setup happens in Profiles only
    // (operator, 2026-09-23). A candidate only fills the field; Save records it.
    if (rootFields.has("game_root")) {
      const look = el("button", { text: t("Look for installed games"), attrs: { type: "button", class: "secondary" } });
      const found = el("ul", { className: "rows", attrs: { "aria-label": t("Installed games found") } });
      look.addEventListener("click", () =>
        withBusy(look, async () => {
          found.replaceChildren(el("li", { className: "muted", text: t("Looking…") }));
          const near = (fields.get("engine")?.input.value || "").trim();
          const query = near ? "?near=" + encodeURIComponent(near.replace(/[^/\\]*$/, "")) : "";
          const { ok, body: out } = await api("/api/v1/engines/detect" + query);
          found.replaceChildren();
          const items = ok ? out.items || [] : [];
          if (!ok || items.length === 0) {
            found.append(el("li", { className: "muted", text: ok ? t("No installed game was found in the usual places. Choose the folder yourself.") : out.error }));
            return;
          }
          for (const candidate of items) {
            const use = el("button", { text: t("Use this folder"), attrs: { type: "button" } });
            use.addEventListener("click", () => {
              rootFields.get("game_root").input.value = candidate.path;
              setMessage(saveStatus, t("Filled in. Press Save these paths to record it."), "");
            });
            found.append(el("li", { children: [
              el("div", { className: "row-head", children: [el("strong", { text: candidate.path }), badge(candidate.source, "queued")] }),
              el("p", { className: "muted", text: `found ${candidate.evidence} in ${candidate.base_dir}` + (candidate.note ? " — " + candidate.note : "") }),
              el("div", { className: "row-actions", children: [use] }),
            ] }));
          }
        })
      );
      detail.append(el("div", { className: "row-actions", children: [look] }), found);
    }

    const save = el("button", { text: "Save these paths", attrs: { type: "button", class: "primary" } });
    const saveStatus = el("p", { className: "message", attrs: { role: "status" } });
    save.addEventListener("click", () =>
      withBusy(save, async () => {
        const paths = {};
        for (const [name, field] of fields) paths[name] = field.input.value.trim();
        const rootPaths = {};
        for (const [role, field] of rootFields) rootPaths[role] = field.input.value.trim();
        const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(body.id)}/bind`, {
          method: "POST",
          body: { executables: paths, roots: rootFields.size ? rootPaths : undefined },
        });
        if (!ok) {
          setMessage(saveStatus, out.error || "could not record these paths", "error");
          return;
        }
        setMessage(saveStatus, "Recorded. It survives a restart.", "ok");
        record(`Recorded where ${body.name} is`, Object.values(paths).filter(Boolean).join(", "), "ok");
        describeProvenance(out.binding);
      })
    );
    useFolder.addEventListener("click", () =>
      withBusy(useFolder, async () => {
        const chosen = folder.input.value.trim();
        if (!chosen) {
          setMessage(folderStatus, "Choose the folder first, with Browse… or by typing it.", "error");
          return;
        }
        const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(body.id)}/bind`, {
          method: "POST",
          body: { folder: chosen },
        });
        if (!ok) {
          setMessage(folderStatus, out.error || "could not use this folder", "error");
          return;
        }
        for (const [name, field] of fields) field.input.value = (out.binding?.executables || {})[name] || "";
        describeProvenance(out.binding);
        setMessage(folderStatus, `Found all ${fields.size} programs. Recorded as a local binding; it survives a restart.`, "ok");
        record(`Recorded where ${body.name} is`, chosen, "ok");
      })
    );
    detail.append(el("div", { className: "row-actions", children: [save] }));
    detail.append(saveStatus);
  }

  // executableNames reads the declared programs off whichever shape the
  // document has. A pipeline declares none and gets no fields, which is right:
  // a pipeline starts nothing of its own.
  function executableNames(body) {
    return body.executables || [];
  }

  // --- the wizard -----------------------------------------------------------

  function setStep(next) {
    step = Math.min(4, Math.max(1, next));
    for (let index = 1; index <= 4; index += 1) {
      $("wizard-step-" + index).classList.toggle("active", index === step);
    }
    const markers = $("wizard-steps").children;
    for (let index = 0; index < markers.length; index += 1) {
      if (index === step - 1) markers[index].setAttribute("aria-current", "step");
      else markers[index].removeAttribute("aria-current");
    }
    $("wizard-back").disabled = step === 1;
    $("wizard-next").disabled = step === 4;
    const kind = $("wizard-kind").value;
    $("wizard-engine-fields").hidden = kind !== "engine";
    $("wizard-tool-fields").hidden = kind !== "tool";
    $("wizard-homepage-field").hidden = kind === "pipeline";
    $("wizard-step-3-title").textContent = kind === "pipeline" ? "Stages and their parameters" : "Programs and actions";
    $("wizard-step-3-note").textContent = kind === "pipeline"
      ? "A pipeline is where parameters are set. Add as many stages as you need, in the order they run; each one picks a tool, says where its inputs come from and carries its own parameters and arguments. The same tool may be a stage more than once."
      : kind === "tool"
        ? "A build tool says where its programs are and what each one can do. What they are run with is set in the pipelines that use them."
        : "The engine's programs, and what each action starts.";
    if (step === 3) window.AUCOM.scratch.render();
  }

  // pinnedTemplate is the template a `#new-profile/<id>` link asked for.
  let pinnedTemplate = "";

  // The starting points for the kind being described: From scratch first and
  // by default, then every tested profile of that kind (operator, 2026-10-03).
  async function refreshTemplates() {
    const kind = $("wizard-kind").value;
    const { ok, body } = await api("/api/v1/profiles/templates?kind=" + encodeURIComponent(kind));
    const select = $("wizard-template");
    select.replaceChildren();
    select.append(el("option", { text: "From scratch", attrs: { value: "" } }));
    if (!ok) {
      setMessage("wizard-message", body.error, "error");
      templates = [];
    } else {
      templates = body.items || [];
    }
    for (const template of templates) {
      select.append(el("option", { text: `${template.name} — ${template.summary}`, attrs: { value: template.id } }));
    }
    select.value = pinnedTemplate && templates.some((template) => template.id === pinnedTemplate) ? pinnedTemplate : "";
    await applyTemplate();
  }

  function currentTemplate() {
    return templates.find((template) => template.id === $("wizard-template").value);
  }

  // The identity fields a starting point fills. What it filled follows the
  // next starting point chosen; what the person typed is theirs and is kept.
  const IDENTITY = {
    "wizard-name": "name", "wizard-version": "version", "wizard-summary": "summary",
    "wizard-publisher": "publisher_name", "wizard-license": "license_spdx", "wizard-homepage": "homepage",
    "wizard-runtime": "runtime", "wizard-engine-version": "engine_version",
  };

  function offer(id, value) {
    const field = $(id);
    if (field.value && field.value !== field.dataset.offered) return;
    field.value = value || "";
    field.dataset.offered = field.value;
  }

  // applyTemplate fills EVERY step's fields from the chosen starting point —
  // identity, programs, actions, stages — or empties them for From scratch.
  async function applyTemplate() {
    const kind = $("wizard-kind").value;
    const template = currentTemplate();
    const scratch = window.AUCOM.scratch;
    if (!template) {
      scratch.clear(kind);
      for (const id of Object.keys(IDENTITY)) offer(id, "");
      $("wizard-template-note").textContent =
        "Nothing is filled in: you write every field. Choose a tested profile above to have all of them filled, and change what is different about yours.";
      scratch.render();
      return;
    }
    const { ok, body } = await api(`/api/v1/profiles/templates/${encodeURIComponent(template.id)}/scratch`);
    if (!ok) {
      setMessage("wizard-message", body.error || "that profile could not be read", "error");
      return;
    }
    scratch.fill(body.scratch);
    for (const [id, key] of Object.entries(IDENTITY)) offer(id, body.identity?.[key]);
    $("wizard-template-note").textContent =
      `Filled in from ${template.name} ${template.version}` + (template.summary ? ` — ${template.summary}` : "") +
      ". Every field in the next steps is yours to change, add to or remove; what the form has no field for is kept as tested.";
    scratch.render();
  }

  function composeRequest(fromDocument) {
    if (fromDocument) {
      // Editing a document the user pasted: the form fields are not applied on
      // top of it, because they belong to a different document and would
      // silently rewrite what somebody pasted.
      return { document: fromDocument, template: $("wizard-template").value };
    }
    const kind = $("wizard-kind").value;
    const value = (id) => $(id).value.trim() || undefined;
    const request = {
      scratch: window.AUCOM.scratch.request(),
      name: value("wizard-name"), version: value("wizard-version"), summary: value("wizard-summary"),
      publisher_name: value("wizard-publisher"), license_spdx: value("wizard-license"),
    };
    if (kind !== "pipeline") request.homepage = value("wizard-homepage");
    if (kind === "engine") {
      request.runtime = value("wizard-runtime");
      request.engine_version = value("wizard-engine-version");
    }
    return request;
  }

  async function compose(fromDocument) {
    const { ok, body } = await api("/api/v1/profiles/compose", {
      method: "POST",
      body: composeRequest(fromDocument),
    });
    if (!ok) {
      setMessage("wizard-message", body.error || "the document could not be composed", "error");
      return null;
    }
    composed = body;
    $("wizard-json").value = JSON.stringify(body.document, null, 2);
    renderReview(body);
    return body;
  }

  function renderReview(body) {
    const review = $("wizard-review");
    review.replaceChildren();
    if (!body.valid) {
      review.append(el("h4", { text: "This document is not valid yet" }));
      review.append(el("pre", { className: "output", text: body.error || "" }));
      $("wizard-import").disabled = true;
      return;
    }
    $("wizard-import").disabled = false;

    const head = el("p");
    head.append(el("strong", { text: `${body.name} ${body.version}` }));
    head.append(document.createTextNode(" "));
    head.append(badge(body.trust));
    review.append(head);
    review.append(
      el("p", {
        className: "muted",
        text:
          "Installing this does not approve it. It will be listed as local — nobody has vouched for it, " +
          "including you — and it cannot run until you have approved it.",
      })
    );

    if (body.diff && !body.diff.empty) {
      review.append(el("h4", { text: "What you changed from the tested profile" }));
      if (body.diff.escalates) {
        review.append(
          el("p", {
            className: "message error",
            text: "This asks for more than the tested profile did.",
          })
        );
      }
      const list = el("ul", { className: "mono" });
      for (const change of body.diff.changes) {
        list.append(
          el("li", {
            className: "diff-" + change.kind,
            text: `${change.kind} ${change.path}: ${change.before ?? ""} → ${change.after ?? ""}`,
          })
        );
      }
      review.append(list);
    }
  }

  $("wizard-kind").addEventListener("change", async () => {
    await refreshTemplates();
    setStep(step);
  });
  $("wizard-template").addEventListener("change", (event) => withBusy(event.currentTarget, applyTemplate));
  $("wizard-back").addEventListener("click", () => setStep(step - 1));
  $("wizard-next").addEventListener("click", async (event) => {
    if (step === 3) {
      await withBusy(event.currentTarget, () => compose(null));
    }
    setStep(step + 1);
  });

  $("wizard-check-json").addEventListener("click", (event) =>
    withBusy(event.currentTarget, async () => {
      let parsed;
      try {
        parsed = JSON.parse($("wizard-json").value);
      } catch (err) {
        $("wizard-json").setAttribute("aria-invalid", "true");
        setMessage("wizard-message", "That is not JSON: " + err.message, "error");
        return;
      }
      $("wizard-json").removeAttribute("aria-invalid");
      const body = await compose(parsed);
      if (body) {
        setMessage(
          "wizard-message",
          body.valid ? "This document is valid." : "This document is not valid yet — see the review above.",
          body.valid ? "ok" : "error"
        );
      }
    })
  );

  $("wizard-copy-json").addEventListener("click", async (event) => {
    const text = $("wizard-json").value;
    try {
      await navigator.clipboard.writeText(text);
      setMessage("wizard-message", "Copied.", "ok");
    } catch {
      // Clipboard access can be refused, and a page that only said "copied"
      // would be lying. Select it instead so the user can copy it themselves.
      $("wizard-json").select();
      setMessage("wizard-message", "This browser would not let the page use the clipboard — the text is selected, copy it yourself.", "");
    }
  });

  $("wizard-download-json").addEventListener("click", (event) =>
    withBusy(event.currentTarget, async () => {
      const { ok, body } = await api("/api/v1/paths/pick", {
        method: "POST",
        body: { kind: "save-file", title: "Save this profile" },
      });
      if (ok && body.cancelled) return;
      if (!ok) {
        setMessage("wizard-message", body.error || "no file chooser is available; copy the text instead", "error");
        return;
      }
      // The Companion writes the file, not the browser: a download would land
      // wherever the browser puts downloads, which is not where the user just
      // said.
      setMessage("wizard-message", `Chosen ${body.path}. Use Copy and save it there — the Companion does not write outside its own directories.`, "");
    })
  );

  $("wizard-load-json").addEventListener("click", (event) =>
    withBusy(event.currentTarget, async () => {
      const { ok, body } = await api("/api/v1/paths/pick", {
        method: "POST",
        body: { kind: "open-file", title: "Open a profile document", filters: [{ name: "Profile documents", extensions: ["json"] }] },
      });
      if (ok && body.cancelled) return;
      if (!ok) {
        setMessage("wizard-message", body.error || "no file chooser is available; paste the document instead", "error");
        return;
      }
      setMessage("wizard-message", `Chosen ${body.path}. Paste its contents below — the page is not given access to your files.`, "");
      $("wizard-json").focus();
    })
  );

  $("wizard-import").addEventListener("click", (event) =>
    withBusy(event.currentTarget, async () => {
      let parsed;
      try {
        parsed = JSON.parse($("wizard-json").value);
      } catch (err) {
        setMessage("wizard-message", "That is not JSON: " + err.message, "error");
        return;
      }
      const { ok, status, body } = await api("/api/v1/profiles/import", {
        method: "POST",
        body: { document: parsed, replace: $("wizard-replace").checked },
      });
      if (!ok) {
        setMessage("wizard-message", body.error || "the profile could not be installed", "error");
        record("Profile import refused", body.error, "failed");
        return;
      }
      setMessage(
        "wizard-message",
        `Installed ${body.name || "the profile"} (${body.trust}, not approved yet) — opening its review and setup in Profiles.`,
        "ok"
      );
      record(`Installed the profile ${body.name || ""}`.trim(), "", "ok");
      // A pipeline's stage arguments are this machine's setup of it, not part
      // of the document: recorded now that there is a profile to record them
      // against. A stage whose tokens are refused says so and loses nothing
      // else — the profile is installed either way.
      if (composed?.id === body.id) {
        for (const item of window.AUCOM.scratch.stageTokens()) {
          const saved = await api(`/api/v1/profiles/${encodeURIComponent(body.id)}/stage-arguments`, { method: "POST", body: item });
          if (!saved.ok) record(`The arguments of stage ${item.stage} were not saved`, saved.body.error || "", "failed");
        }
      }
      await window.AUCOM.openInstalledProfile(body.id);
    })
  );

  $("profiles-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshList));
  $("profiles-kind").addEventListener("change", refreshList);
  $("profile-detail-close").addEventListener("click", () => window.AUCOM.showArea("profiles"));
  $("profile-breadcrumb-up").addEventListener("click", (event) => {
    event.preventDefault();
    window.AUCOM.showArea("profiles");
  });

  // New profile opens both ways of writing one — from a tested template, or
  // from scratch — at the top of the area, where the button is.
  // After an install on the New profile page, this tab becomes Profiles with
  // the new document's review open: approving it and saying where its programs
  // are is the next thing to do, and both live there.
  window.AUCOM.openInstalledProfile = async (id) => {
    document.body.classList.remove("single-view");
    window.AUCOM.showArea("profiles");
    await refreshList();
    const found = installed.find((item) => item.id === id);
    if (!found) {
      // The installed list is one kind at a time; show the kind it is.
      for (const candidate of ["tool", "engine", "pipeline"]) {
        $("profiles-kind").value = candidate;
        await refreshList();
        if (installed.some((item) => item.id === id)) break;
      }
      $("profiles-kind").dispatchEvent(new Event("change", { bubbles: true }));
    }
    configure(id);
  };

  // New profile opens the creator in a new browser tab: the page served there
  // carries this run's own token, so it is the same Companion, and the
  // installed list stays where the person left it (NEW_244D, operator).
  $("profiles-new").addEventListener("click", () => {
    window.open(window.location.pathname + "?view=new-profile#new-profile", "_blank", "noopener");
  });

  let creatorFor = null; // the starting point the form was last opened on
  window.AUCOM.areas["new-profile"] = {
    // `#new-profile/<template id>` opens the creator filled in from that
    // tested profile — the "Make your own copy" of a built-in profile's page.
    // Opened again on the same starting point it keeps what was typed.
    async refresh(argument) {
      pinnedTemplate = argument ? decodeURIComponent(argument) : "";
      // Which tools are installed decides what a pipeline stage can pick.
      await window.AUCOM.scratch.refresh();
      if (creatorFor === pinnedTemplate) {
        setStep(step);
        return;
      }
      creatorFor = pinnedTemplate;
      if (pinnedTemplate) {
        const { ok, body } = await api("/api/v1/profiles/templates");
        const found = ok ? (body.items || []).find((item) => item.id === pinnedTemplate) : null;
        if (found) $("wizard-kind").value = found.kind;
      }
      await refreshTemplates();
      setStep(pinnedTemplate ? 1 : step);
    },
  };

  window.AUCOM.areas.profiles = {
    open: openProfile,
    configure,
    async refresh(argument) {
      // `#profiles/<id>` is one profile's configuration page, so a reload comes
      // back to that profile rather than to the list — the shape `#games/<id>`
      // already established.
      showOverview(!argument);
      if (argument) {
        await openProfile(decodeURIComponent(argument));
        window.scrollTo({ top: 0 });
        return;
      }
      await refreshList();
    },
  };
})();
