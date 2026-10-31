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
  let draftGeneration = 0;
  let templateRequest = 0;
  let templateReady = true;
  let composeSequence = 0;
  let validatedKey = "";
  let validatedStageTokens = [];
  let pendingInstallation = null;
  // unsavedStages is what the New profile form could not save after installing
  // a pipeline: { id, unsaved: [{stage, arguments}], problem: {stage, message} }.
  // That pipeline's page opens with those tokens in its stage editors, not yet
  // saved, and the refusal beside the stage it names (NEW_360).
  let unsavedStages = null;
  let installing = false;

  function invalidateDraft() {
    draftGeneration += 1;
    composed = null;
    validatedKey = "";
    $("wizard-import").disabled = true;
    $("wizard-review").replaceChildren();
  }

  function draftKey() {
    return JSON.stringify([composeRequest(null), window.AUCOM.scratch.stageTokens()]);
  }

  function fieldError(id, message) {
    const input = $(id);
    input.setAttribute("aria-invalid", "true");
    let note = $(id + "-error");
    if (!note) {
      note = el("span", { className: "message error wizard-field-error", attrs: { id: id + "-error" } });
      input.after(note);
    }
    note.textContent = message;
    input.setAttribute("aria-describedby", note.id);
    return input;
  }

  function validateStep() {
    for (const note of document.querySelectorAll(".wizard-field-error")) note.remove();
    for (const input of document.querySelectorAll('#area-new-profile [aria-invalid]')) {
      input.removeAttribute("aria-invalid");
    }
    const errors = [];
    if (step === 1 && $("wizard-kind").value === "engine" && !$("scratch-game").value) {
      errors.push(fieldError("scratch-game", "Choose the engine's game family."));
    }
    if (step === 2) {
      for (const [id, title] of [["wizard-name", "Name"], ["wizard-version", "Version"],
        ["wizard-summary", "Summary"], ["wizard-publisher", "Publisher"], ["wizard-license", "Licence"]]) {
        if (!$(id).value.trim()) errors.push(fieldError(id, title + " is required."));
      }
    }
    if (step === 3 && $("wizard-kind").value === "pipeline" && window.AUCOM.scratch.pendingReview?.()) {
      // A tool change is waiting for an answer inside the stage. Moving on
      // would answer it for the person, one way or the other.
      setMessage("wizard-message", `Stage ${window.AUCOM.scratch.pendingReview()} has a tool change waiting: keep the current tool or change it, then continue.`, "error");
      $("scratch-body").querySelector('[data-switch="keep"]')?.focus();
      return false;
    }
    if (errors.length) {
      setMessage("wizard-message", "Complete the marked fields before continuing.", "error");
      errors[0].focus();
      return false;
    }
    setMessage("wizard-message", "");
    return true;
  }


  // --- installed profiles ---------------------------------------------------
  //
  // NEW_364: the list answers three questions a person has — what is this
  // for, what must I configure, can I use it now — in one row each: the name
  // (which opens its page), the kind and game, and ONE state the server
  // decided (`setup_state`, core.js `profileState`). Trust, provenance,
  // versions and digests are on the profile's own page, under Details.

  // Fences: an answer belongs to the request that is still current. A list
  // asked for one kind and answered after another was chosen, a page asked
  // for profile A and answered after B was opened, or anything asked before
  // the account or server changed, is dropped (`aucom:status`).
  let lastOpened = "";
  let listSequence = 0;
  let detailSequence = 0;
  let viewGeneration = 0;

  async function refreshList() {
    const sequence = ++listSequence;
    const generation = viewGeneration;
    const kind = $("profiles-kind").value;
    const { ok, body } = await api("/api/v1/profiles" + (kind ? "?kind=" + encodeURIComponent(kind) : ""));
    if (sequence !== listSequence || generation !== viewGeneration || kind !== $("profiles-kind").value) return;
    if (!ok) {
      $("profiles-list").replaceChildren();
      setMessage("profiles-message", body.error || t("The profiles could not be read."), "error");
      return;
    }
    installed = body.items || [];
    drawList();
  }

  const KIND_NAMES = { tool: "Build tool", pipeline: "Build pipeline", engine: "Game engine" };
  const GAME_NAMES = { quake1: "Quake", quake2: "Quake II", quake3: "Quake III" };
  function kindAndGame(profile) {
    const game = GAME_NAMES[profile.engine_family] || "";
    return t(KIND_NAMES[profile.kind] || profile.kind) + (game ? " · " + t(game) : "");
  }

  // drawList filters by the search words (name, summary, kind, game) without
  // asking the server again: the list is a few dozen rows.
  function drawList() {
    const list = $("profiles-list");
    const words = $("profiles-search").value.trim().toLowerCase().split(/\s+/).filter(Boolean);
    const shown = installed.filter((profile) => {
      const text = [profile.name, profile.summary, kindAndGame(profile)].join(" ").toLowerCase();
      return words.every((word) => text.includes(word));
    });
    list.replaceChildren(...shown.map(profileRow));
    if (!installed.length) setMessage("profiles-message", t("No profile of this kind is installed."));
    else if (!shown.length) setMessage("profiles-message", t("No profile matches “{query}”.", { query: $("profiles-search").value.trim() }));
    else setMessage("profiles-message", "");
  }

  function stateBadge(profile) {
    const state = window.AUCOM.profileState(profile);
    return state ? badge(state.text, state.kind) : document.createTextNode("");
  }

  // One row: the name is the link to the profile's page (and the whole row
  // is its target), so there is no Configure button per row saying the same.
  function profileRow(profile) {
    const state = window.AUCOM.profileState(profile);
    const link = el("a", { className: "profile-row__name", text: profile.name,
      attrs: { href: "#profiles/" + encodeURIComponent(profile.id), title: profile.name } });
    const kind = el("span", { className: "profile-row__kind muted", text: kindAndGame(profile) });
    const wip = maturityBadge(profile.maturity);
    if (wip) kind.append(document.createTextNode(" "), wip);
    const status = el("span", { className: "profile-row__state" });
    if (state) {
      status.append(badge(state.text, state.kind));
      // A pipeline waits for a TOOL: the row names that tool, once.
      if (state.cause) status.append(el("span", { className: "muted small", text: " " + state.cause.name }));
    }
    const install = window.AUCOM.installButton?.(profile.id);
    if (install) status.append(" ", install);
    return el("li", { className: "profile-row", attrs: { "data-profile": profile.id, "data-state": state?.code || "" },
      children: [link, kind, status] });
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

  // homepageSection is where a profile's homepage is read and — for a profile
  // of this machine's own — changed (operator, 2026-09-23). A built-in document
  // is part of the program, so its answer is a copy of one's own, made in New
  // profile from it. An edit is a new document version with a new digest, so
  // it has to be approved again; the page says so before and after.
  function homepageSection(body) {
    const section = el("div", { className: "profile-homepage" });
    section.append(el("h5", { text: t("Homepage") }));
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
    // Saving it changes the document, so it says so on the button: this is
    // not an innocent Save (NEW_364).
    const save = el("button", { text: t("Save homepage (needs approval again)"), attrs: { type: "button", class: "secondary" } });
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
      })
    );
    section.append(
      el("div", { className: "field", children: [input] }),
      el("div", { className: "row-actions", children: [save] }),
      status,
    );
    return section;
  }

  // configure opens one profile's page IN this window, under Profiles:
  // `#profiles/<id>`, a hash that survives a reload and names the profile and
  // nothing else (operator, 2026-09-23: the side panel stays).
  function configure(id) {
    window.AUCOM.showArea("profiles/" + encodeURIComponent(id));
  }

  // showOverview switches the area between the list and one profile's page.
  function showOverview(overview) {
    $("profiles-overview").hidden = !overview;
    $("profile-breadcrumb").hidden = overview;
    $("profile-detail-panel").hidden = overview;
    $("profiles-new").hidden = !overview;
  }

  function section(title, id, children = []) {
    return el("section", { className: "profile-section", attrs: { id, "aria-labelledby": id + "-title" },
      children: [el("h4", { text: title, attrs: { id: id + "-title" } }), ...children] });
  }

  async function openProfile(id) {
    const sequence = ++detailSequence;
    const generation = viewGeneration;
    const panel = $("profile-detail-panel");
    const detail = $("profile-detail");
    panel.hidden = false;
    detail.replaceChildren(el("p", { className: "muted", text: t("Reading…") }));

    const { ok, body } = await api("/api/v1/profiles/" + encodeURIComponent(id));
    // A late answer for a profile that is no longer the one open (or for
    // before the account changed) draws nothing.
    if (sequence !== detailSequence || generation !== viewGeneration) return;
    detail.replaceChildren();
    if (!ok) {
      detail.append(el("p", { className: "message error", text: body.error }));
      return;
    }
    $("profile-detail-title").textContent = body.name;
    $("profile-detail-title").setAttribute("tabindex", "-1");
    $("profile-detail-title").focus();

    // Header: kind and game, and the one state.
    const head = el("p", { className: "profile-meta", attrs: { id: "profile-state" } });
    head.append(el("span", { className: "muted", text: kindAndGame(body) + " " }), stateBadge(body));
    detail.append(head);
    // Above everything it is about: a warning that arrives after the thing it
    // is about is read after the decision.
    const note = maturityNote(body.maturity, () =>
      openCompatibilityReport({
        family: body.engine_family,
        about: `About ${body.name}.`,
        profiles: [{ role: body.kind === "engine" ? "engine" : body.kind, id: body.id, version: body.version }],
      })
    );
    if (note) detail.append(note);
    // Purpose: one sentence from the document, no history.
    if (body.summary) detail.append(el("p", { className: "profile-purpose", text: body.summary }));

    const handoff = unsavedStages && unsavedStages.id === body.id ? unsavedStages : null;
    unsavedStages = null;
    const details = el("details", { className: "profile-details", attrs: { id: "profile-details" } });
    details.append(el("summary", { text: t("Details") }));

    // An unapproved profile is reviewed before it is set up: approving is its
    // next step, and its setup cannot make it run before that.
    const approval = approvalSection(body);
    if (approval && !body.authorized) detail.append(approval);
    detail.append(body.kind === "pipeline" ? pipelineSetup(body) : setupSection(body, details));
    const options = optionsSection(body, handoff);
    if (options) detail.append(options);
    if (approval && body.authorized) detail.append(approval);

    fillDetails(details, body);
    detail.append(details);
    const refused = detail.querySelector('.args-editor[data-refused="true"]');
    if (refused) {
      refused.closest("details").open = true;
      refused.scrollIntoView({ block: "center" });
      refused.querySelector("input")?.focus();
    }
  }

  // The trust boundary, shown when there is one: a built-in document runs
  // without an approval (installing the Companion was the decision), so it
  // gets no section — the sentence saying so is under Details.
  function approvalSection(body) {
    if (body.authorized && body.trust === "builtin") return null;
    const status = el("p", { className: "message", attrs: { role: "status" } });
    const buttons = el("div", { className: "row-actions" });
    // `authorized` is profile.Authorize's own answer, not a guess assembled
    // from trust and grant. A signature says who published something, never
    // that you agreed to it.
    if (body.authorized) {
      status.textContent = t("Approved on {when}.", { when: when(body.binding?.granted_at) });
      status.className = "message ok";
      const withdraw = el("button", { text: t("Withdraw approval"), attrs: { type: "button", class: "danger" } });
      withdraw.addEventListener("click", () =>
        withBusy(withdraw, async () => {
          const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(body.id)}/withdraw`, { method: "POST" });
          if (!ok) {
            setMessage(status, out.error, "error");
            return;
          }
          record(`Withdrew approval for ${body.name || "the profile"}`, "", "cancelled");
          await openProfile(body.id);
        })
      );
      buttons.append(withdraw);
    } else {
      // Never done for anybody: the digest of the document as it is now goes
      // back with it, and the server refuses one that is not on disk.
      status.textContent = t("Not approved on this machine yet. Read what it asks for above, then approve it.");
      status.className = "message error";
      const approve = el("button", { text: t("I have read this — approve it"), attrs: { type: "button", class: "primary" } });
      approve.addEventListener("click", () =>
        withBusy(approve, async () => {
          const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(body.id)}/grant`, {
            method: "POST",
            body: { digest: body.digest },
          });
          if (!ok) {
            setMessage(status, out.error, "error");
            return;
          }
          record(`Approved ${body.name || "the profile"}`, "", "ok");
          await openProfile(body.id);
        })
      );
      buttons.append(approve);
    }
    const what = el("ul", { className: "plain small", children: describeAsks(body) });
    return section(t("Approval"), "profile-approval", [what, status, buttons]);
  }

  // What approving lets it do, in a person's words: the programs it runs and
  // the folders it reaches. The permission report's ids stay on the wire.
  function describeAsks(body) {
    const items = [];
    if (body.kind === "pipeline") {
      for (const stage of body.stages || []) {
        items.push(el("li", { text: `${stage.title || stage.id}: ${stage.tool?.executable || stage.capability}` }));
      }
      return items;
    }
    const programs = (body.executables || []).map((executable) => executable.name);
    if (programs.length) items.push(el("li", { text: t("Runs: {programs}", { programs: window.AUCOM.listOf(programs) }) }));
    const roles = [...new Set((body.actions || []).flatMap((action) => (action.roots || []).map((root) => root.role)))];
    if (roles.length) items.push(el("li", { text: t("Reaches: {folders}", { folders: window.AUCOM.listOf(roles.map((role) => window.AUCOM.folderTitle(role))) }) }));
    if ((body.actions || []).some((action) => action.network)) items.push(el("li", { text: t("Uses the network") }));
    return items;
  }

  // --- Setup ------------------------------------------------------------------

  // A tool or an engine: its state in one line, what to do about it, and the
  // pickers. The checker's own sentences are a click away, never gone.
  function setupSection(body, details) {
    const part = section(t("Setup"), "profile-setup-section");
    if (body.readiness) part.append(readinessBox(body));
    renderSetup(part, body, details);
    return part;
  }

  // The trust decision is the Approval section's to say; Setup says what is
  // left to choose on this machine (one fact, one statement).
  const APPROVAL_FAULTS = new Set(["not_authorized", "stale_binding"]);
  function withoutApproval(body) {
    const problems = (body.readiness?.problems || []).filter((problem) => !APPROVAL_FAULTS.has(problem.fault));
    return { ...body, readiness: { ...body.readiness, problems } };
  }

  function readinessBox(full) {
    const box = el("div", { className: "readiness", attrs: { id: "profile-readiness" } });
    const state = window.AUCOM.profileState(full);
    const body = withoutApproval(full);
    // "Ready" is every action; one that cannot start yet is named with what
    // it needs (NEW_330), never hidden under "everything is here".
    const blocked = Object.entries(body.action_problems || {}).filter(([, problems]) => (problems || []).length);
    if (state?.code === "ready") {
      box.append(el("p", { className: "message ok", text: t("Ready: everything its programs need is on this machine.") }));
    } else if (state?.code === "partly_ready") {
      const title = (id) => (body.actions || []).find((action) => action.id === id)?.title || id;
      box.append(el("p", { className: "message warning", text: t("Partly ready: some of what it does can start here, and these cannot yet:") }));
      box.append(el("ul", {
        className: "plain",
        children: blocked.map(([id, problems]) => el("li", {
          text: `${t(title(id))}: ${shortCauses({ ...body, readiness: { problems }, action_problems: {} })}`,
        })),
      }));
    } else if (nothingChosenYet(body)) {
      // A program nobody has pointed at yet is not an error, and one sentence
      // names them all (NEW_345).
      const programs = body.compatibility.map((row) => row.executable);
      box.append(el("p", { className: "readiness__state", children: [
        document.createTextNode(t("{programs}: no path yet. Choose where they are below.", { programs: window.AUCOM.listOf(programs) })),
      ] }));
    } else if (body.readiness.problems.length) {
      // What is wrong, by program, and the next thing to do: the pickers
      // below for something not chosen yet, the checker's own fix for a
      // program that is there and is the wrong one.
      const fixes = state?.code === "needs_setup" || state?.code === "needs_approval" ? [t("Choose where below.")]
        : [...new Set((body.readiness.problems || []).map((problem) => problem.fix).filter(Boolean))].slice(0, 1);
      box.append(el("p", { className: "readiness__problem", text: [shortCauses(body), ...fixes].join(" ") }));
    }
    const exact = (body.readiness.problems?.length ? body.readiness.problems : blocked.flatMap(([, problems]) => problems))
      .filter((problem) => !APPROVAL_FAULTS.has(problem.fault));
    if (exact.length) {
      box.append(el("details", { className: "profile-about", children: [
        el("summary", { text: t("What the checker said") }),
        el("ul", { className: "plain small", children: exact.map((problem) =>
          el("li", { text: [problem.summary, problem.fix].filter(Boolean).join(" ") })) }),
      ] }));
    }
    // What runs, by what it said it is: "Using …" when the programs answered.
    // Said only when every program that has a path answered as one this
    // profile accepts: a half-right "Using" would read as all right.
    const bound = (body.compatibility || []).filter((row) => row.state !== "not_bound");
    const using = bound.filter((row) => row.state === "compatible" && row.identity);
    if (using.length && using.length === bound.length) {
      const identities = [...new Set(using.map((row) => row.identity))];
      box.append(el("p", { className: "muted small profile-using", text: identities.length === 1
        ? t("Using: {version}", { version: identities[0] })
        : t("Using: {version}", { version: using.map((row) => `${row.executable} ${row.identity}`).join(", ") }) }));
    }
    return box;
  }

  // shortCauses: what is wrong, grouped by program and fault (core.js
  // setupCauses), without the profile's own name in front of it — this page
  // is that profile.
  function shortCauses(body) {
    return window.AUCOM.setupCauses(body).map((cause) =>
      cause.profile === body.id && cause.text.startsWith(cause.name + ": ") ? cause.text.slice(cause.name.length + 2) : cause.text).join(" ");
  }

  // nothingChosenYet: nothing on this machine names a program or a folder for
  // it, and nothing else is wrong — every tool on a fresh install.
  function nothingChosenYet(body) {
    const rows = body.compatibility || [];
    const recorded = Object.values(body.binding?.executables || {}).some(Boolean) ||
      Object.values(body.binding?.roots || {}).some(Boolean);
    return !recorded && rows.length > 0 && rows.every((row) => row.state === "not_bound") &&
      (body.readiness?.problems || []).every((problem) => problem.fault === "missing_engine" || APPROVAL_FAULTS.has(problem.fault));
  }

  // A pipeline starts nothing of its own: its setup is its stages' tools.
  // What waits is said once per TOOL that has to be set up, with the link to
  // that tool's page (NEW_345 `problemOrigin`) — a missing light names the
  // tool that owns light, not three copies of a folder instruction.
  function pipelineSetup(body) {
    const part = section(t("Setup"), "profile-setup-section");
    const box = el("div", { className: "readiness", attrs: { id: "profile-readiness" } });
    const state = window.AUCOM.profileState(body);
    if (state?.code === "ready") {
      box.append(el("p", { className: "message ok", text: t("Ready: every stage's tool is set up.") }));
    } else {
      // The state is in the header; here is what to do, once per tool.
      box.append(el("ul", { className: "plain", children: window.AUCOM.setupCauses(body).map((cause) =>
        el("li", { attrs: { "data-cause": cause.profile }, children: [document.createTextNode(cause.text + " "), window.AUCOM.setupLink(cause)] })) }));
    }
    part.append(box);
    const stages = el("ul", { className: "plain profile-stages" });
    for (const stage of body.stages || []) {
      const tool = stage.tool;
      stages.append(el("li", { children: [
        el("strong", { text: tool?.executable || stage.capability }), document.createTextNode(" — " + (stage.title || stage.id) + " · "),
        el("span", { className: "muted", text: tool ? tool.profile_name
          : stage.named_tool ? t("needs the tool {tool}, which is not installed", { tool: stage.named_tool })
            : t("no installed tool provides {capability}", { capability: stage.capability }) }),
      ] }));
    }
    part.append(stages);
    return part;
  }

  // --- Options ----------------------------------------------------------------
  //
  // Normal options are what the document sets; a program's own extra words
  // are under "Advanced options", one list per program of a pipeline's stage
  // (operator, 2026-10-03: "in pipelines you pick a tool and add the
  // parameters") or per program of an engine. A build tool takes no new ones;
  // ones recorded on it before stage arguments existed can only be removed.
  // Each box is one argument exactly as typed — nothing is split, quoted or
  // passed through a shell, which is why a path with spaces is one box.
  function optionsSection(body, handoff) {
    const editors = [];
    const normal = [];
    if (body.kind === "pipeline") {
      for (const stage of body.stages || []) {
        const program = stage.tool?.executable || stage.id;
        const set = Object.entries(stage.options || {});
        if (set.length) normal.push(el("li", { children: [el("strong", { text: program }), document.createTextNode(` (${stage.title || stage.id}): ` + set.map(([name, value]) => `${name} = ${value}`).join(", "))] }));
        const notes = [];
        if ((stage.tool_arguments || []).length > 0) {
          notes.push(el("p", { className: "message small", text: t("{tool} also has arguments of its own recorded earlier, which reach this stage first: {tokens}. Remove them on the tool's page to keep everything here.", { tool: stage.tool?.profile_name || "", tokens: stage.tool_arguments.join(" ") }) }));
        }
        const unsaved = handoff?.unsaved?.find((item) => item.stage === stage.id);
        const refused = handoff?.problem?.stage === stage.id ? handoff.problem.message : "";
        editors.push(argumentsEditor({
          profile: body, legend: `${program} — ${stage.title || stage.id}`, name: stage.id, program,
          saved: stage.arguments || [], tokens: unsaved ? unsaved.arguments : stage.arguments || [],
          route: "stage-arguments", key: "stage", notes, stage: true,
          read: (out) => (out.stages || []).find((item) => item.id === stage.id)?.arguments || [],
          initial: refused
            ? { text: t("Installed, but these arguments were not saved: {reason} Correct them and press Save arguments.", { reason: refused }), kind: "error" }
            : null,
        }));
      }
    } else {
      const saved = body.binding?.arguments || {};
      const tool = body.kind === "tool";
      for (const executable of executableNames(body)) {
        if (tool && !(saved[executable.name] || []).length) continue;
        editors.push(argumentsEditor({
          profile: body, legend: `${executable.name} — ${executable.title || executable.name}`, name: executable.name, program: executable.name,
          saved: saved[executable.name] || [], tokens: saved[executable.name] || [], route: "arguments", key: "executable",
          read: (out) => out.binding?.arguments?.[executable.name] || [], removeOnly: tool,
        }));
      }
    }
    if (!editors.length && !normal.length) return null;
    const part = section(t("Options"), "profile-options");
    if (normal.length) part.append(el("ul", { className: "plain small profile-normal-options", children: normal }));
    if (editors.length) {
      const advanced = el("details", { className: "advanced-options", attrs: { id: "profile-advanced-options" } });
      advanced.open = editors.some((editor) => editor.dataset.hasTokens === "true");
      advanced.append(el("summary", { text: t("Advanced options") }));
      advanced.append(el("p", { className: "muted small", text: body.kind === "tool"
        ? t("Arguments recorded on this tool earlier still reach every pipeline that runs it. They can be removed here; new ones go on a pipeline's stage.")
        : t("Extra arguments for one program, one per box, added before its input files. Example: -nocolor") }));
      advanced.append(...editors);
      part.append(advanced);
    }
    return part;
  }

  // One program's list of argument tokens. Add and Clear change only this
  // list and only on the page — visibly not saved — until Save arguments is
  // pressed; Save is enabled only while there is something to save. An
  // answer is applied to the revision it was for: an edit made while a save
  // was on its way is kept and stays "not saved" (NEW_364).
  function argumentsEditor({ profile: body, legend, name, program, saved, tokens, route, key, read, notes = [], removeOnly = false, stage = false, initial = null }) {
    const id = (stage ? "profile-stage-args-" : "profile-args-") + name;
    const fieldset = el("fieldset", { className: "args-editor", attrs: { id, "data-program": program } });
    fieldset.append(el("legend", { text: legend }));
    for (const note of notes) fieldset.append(note);
    const list = el("ol", { className: "args-editor__list", attrs: { "aria-label": t("Arguments for {program}", { program }) } });
    const status = el("p", { className: "message", attrs: { role: "status", id: id + "-status" } });
    let baseline = [...saved];
    let refusedFor = null;
    let savedNote = false;

    const values = () => [...list.querySelectorAll("input")].map((input) => input.value);
    const same = (a, b) => a.length === b.length && a.every((value, index) => value === b[index]);
    const markInvalid = (on) => {
      for (const input of list.querySelectorAll("input")) {
        if (on) input.setAttribute("aria-invalid", "true"); else input.removeAttribute("aria-invalid");
      }
    };
    // `controlsOnly` updates the buttons and leaves the message a save's
    // answer just wrote; an edit (any other call) says what is unsaved.
    const refresh = (controlsOnly = false) => {
      const dirty = !same(values(), baseline);
      save.disabled = !dirty;
      fieldset.dataset.dirty = String(dirty);
      clear.disabled = list.children.length === 0;
      if (controlsOnly === true) return;
      if (refusedFor && !same(values(), refusedFor)) { refusedFor = null; markInvalid(false); }
      if (dirty && !refusedFor) { savedNote = false; setMessage(status, t("Not saved yet.")); }
      else if (!dirty && !savedNote && !refusedFor) setMessage(status, "");
    };

    const addRow = (value = "") => {
      const input = el("input", {
        attrs: { type: "text", spellcheck: "false", autocomplete: "off", "aria-label": t("Argument for {program}", { program }),
          "aria-describedby": status.id, "data-args-for": name },
      });
      input.value = value;
      if (removeOnly) input.readOnly = true;
      input.addEventListener("input", refresh);
      const remove = el("button", { text: t("Remove"), attrs: { type: "button", class: "secondary", "aria-label": t("Remove this argument for {program}", { program }) } });
      const row = el("li", { className: "args-editor__row", children: removeOnly ? [input] : [input, remove] });
      remove.addEventListener("click", () => {
        const next = row.nextElementSibling?.querySelector("input") || row.previousElementSibling?.querySelector("input");
        row.remove();
        refresh();
        (next || add).focus();
      });
      list.append(row);
      return input;
    };
    for (const token of tokens) addRow(token);
    fieldset.dataset.hasTokens = String(tokens.length > 0 || Boolean(initial));

    const add = el("button", { text: t("Add argument"), attrs: { type: "button", class: "secondary" } });
    add.addEventListener("click", () => { addRow("").focus(); refresh(); });
    const save = el("button", { text: t("Save arguments"), attrs: { type: "button", class: "primary" } });
    const clear = el("button", { text: removeOnly ? t("Remove these arguments") : t("Clear"), attrs: { type: "button", class: "secondary" } });

    // Only the newest save's answer decides what the page says; an older one
    // that arrives after it changes nothing on the page.
    let saveSequence = 0;
    const send = async (sent, payload) => {
      const sequence = ++saveSequence;
      await withBusy(removeOnly ? clear : save, async () => {
        const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(body.id)}/${route}`, {
          method: "POST", body: { [key]: name, ...payload },
        });
        if (sequence !== saveSequence) return;
        const current = same(values(), sent);
        if (!ok) {
          // The server's own words, beside this program's boxes; the boxes
          // keep what was typed so it can be corrected, never resent as is.
          if (current) { refusedFor = sent; markInvalid(true); }
          setMessage(status, out.error || t("The arguments could not be saved."), "error");
          if (current) list.querySelector("input")?.focus();
          return;
        }
        baseline = read(out);
        if (current) {
          list.replaceChildren();
          for (const token of baseline) addRow(token);
          markInvalid(false);
          savedNote = true;
          refresh(true);
          setMessage(status, baseline.length ? t("Saved.") : t("Saved. {program} runs with the profile's own arguments only.", { program }), "ok");
        } else {
          refresh(true);
          setMessage(status, t("Saved what was there when you pressed Save; your later changes are not saved yet."));
        }
        record(`${program}: ${baseline.length ? baseline.join(" ") : "no arguments of your own"}`, body.name, "ok");
        const previews = document.querySelector("#profile-details .profile-arguments__previews");
        if (previews && document.getElementById(id) === fieldset) {
          if (out.commands) renderPreviewItems(previews, out.commands); else refreshPreviews(body.id, previews);
        }
      });
      // withBusy puts the button back as it was before the press; whether
      // there is anything left to save is this list's own answer.
      if (sequence === saveSequence) refresh(true);
    };

    save.addEventListener("click", () => { const sent = values(); send(sent, { arguments: sent }); });
    clear.addEventListener("click", () => {
      if (removeOnly) { send([], { reset: true }); return; }
      list.replaceChildren();
      refresh();
      add.focus();
    });

    fieldset.append(list, el("div", { className: "row-actions", children: removeOnly ? [clear] : [add, clear, save] }), status);
    refresh();
    if (initial) {
      setMessage(status, initial.text, initial.kind);
      if (initial.kind === "error") { refusedFor = values(); markInvalid(true); fieldset.dataset.refused = "true"; }
    }
    return fieldset;
  }

  // --- Details ----------------------------------------------------------------
  //
  // Closed by default: the document's own account, who published it and
  // under which licence, trust, the exact programs (version, path, digest)
  // with Check these programs, the homepage, the exact commands and, for a
  // profile of this machine's own, removing it.
  function fillDetails(details, body) {
    if (body.description) details.append(el("p", { className: "muted", text: body.description }));
    details.append(el("p", { className: "muted", text: t("Version {version}", { version: body.version }) +
      (body.publisher?.name ? " · " + t("published by {name}", { name: body.publisher.name }) : "") +
      (body.license?.spdx ? ` · ${body.license.spdx}` : "") }));
    const trust = el("p");
    trust.append(badge(body.trust), document.createTextNode(" " + (body.trust_description || "")));
    if (body.authorized && body.trust === "builtin") {
      trust.append(document.createTextNode(" " + t("This document arrived inside the Companion, so installing the program was the decision. There is nothing else to approve.")));
    }
    details.append(trust);
    if (body.kind !== "pipeline") details.append(provenanceLine(body));
    if ((body.compatibility || []).length) details.append(compatibilityBox(body));
    if (body.kind !== "pipeline" && (body.actions || []).length) {
      details.append(el("h5", { text: t("What it does") }),
        el("ul", { className: "plain small", children: body.actions.map((action) => el("li", { text: t(action.title || action.id) })) }));
    }
    if (body.kind === "engine" || body.kind === "tool") details.append(homepageSection(body));
    // The exact commands, from the same planning a job uses; a stage's own
    // arguments are marked.
    if (body.kind !== "pipeline" || (body.stages || []).length) {
      const previews = el("div", { className: "profile-arguments__previews" });
      details.append(el("h5", { text: t("The commands, as they will run") }),
        el("p", { className: "muted small", text: t("Your own arguments are highlighted. Parts in angle brackets are filled in when a job runs — its folder, its input files, the map.") }),
        previews);
      refreshPreviews(body.id, previews);
    }
    if (body.editable) details.append(removeControl(body));
  }

  // Removing a profile is said as removing, in Details, never behind a Close.
  // The server keeps its own safeguards (a profile something still names).
  function removeControl(body) {
    const status = el("p", { className: "message", attrs: { role: "status" } });
    const remove = el("button", { text: t("Remove this profile"), attrs: { type: "button", class: "danger" } });
    remove.addEventListener("click", () =>
      withBusy(remove, async () => {
        const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(body.id)}/remove`, { method: "POST" });
        if (!ok) {
          setMessage(status, out.error, "error");
          return;
        }
        record(`Removed the profile ${body.id}`, out.was, "cancelled");
        window.AUCOM.showArea("profiles");
      })
    );
    return el("div", { className: "profile-remove", children: [el("div", { className: "row-actions", children: [remove] }), status] });
  }

  // Provenance, in words. A program somebody named is not a verified download,
  // and the builtin badge must not read as if it were (NEW_244D).
  function provenanceLine(body) {
    const provenance = el("p", { className: "provenance small", attrs: { id: "profile-provenance" } });
    const named = Object.values(body.binding?.executables || {}).filter(Boolean).length;
    if (named > 0) {
      provenance.append(badge("local binding", "local"),
        document.createTextNode(" " + t("Programs you named on this machine. The Companion runs what you pointed at and downloads nothing.") +
          (body.trust === "builtin" ? " " + t("The profile's builtin badge is about the document, not about these bytes.") : "")));
    }
    return provenance;
  }

  // compatibilityBox is what each program said it is when the Companion asked
  // it (NEW_323D): one row per program with the answer the SERVER recorded,
  // its path and digest. "Check these programs" asks again — after a compiler
  // was replaced where it stood — and changes no path and no approval. It is
  // never asked on a poll.
  const COMPATIBILITY_STATES = {
    compatible: ["matches this profile", "ok"],
    incompatible: ["not the version this profile was written for", "blocked"],
    unknown_output: ["did not say what it is", "blocked"],
    timed_out: ["did not answer", "blocked"],
    missing: ["not there", "blocked"],
    not_permitted: ["may not be run", "blocked"],
    wrong_architecture: ["not a program for this computer", "blocked"],
    failed: ["could not be asked", "blocked"],
    not_checked: ["not checked since it changed", "queued"],
    not_bound: ["no path yet", "queued"],
  };
  function compatibilityBox(body) {
    const section = el("div", { className: "compatibility", attrs: { id: "profile-compatibility" } });
    section.append(el("h5", { text: t("What each program said it is when the Companion asked it:") }));
    section.append(el("ul", {
      className: "plain small",
      children: body.compatibility.map((row) => {
        const [label, kind] = COMPATIBILITY_STATES[row.state] || [row.state, "blocked"];
        const item = el("li", { attrs: { "data-executable": row.executable, "data-state": row.state } });
        item.append(el("strong", { text: row.executable }), document.createTextNode(" — "));
        item.append(document.createTextNode(row.identity ? t("version {version}", { version: row.identity }) + " " : ""));
        item.append(badge(t(label), kind));
        if (row.path) item.append(el("span", { className: "muted profile-path", text: " " + row.path }));
        if (row.sha256) item.append(el("span", { className: "muted profile-path", text: " " + row.sha256 }));
        return item;
      }),
    }));
    if (body.compatibility.some((row) => row.state !== "not_bound")) {
      const check = el("button", { text: t("Check these programs"), attrs: { type: "button", class: "secondary", id: "profile-check-programs" } });
      const status = el("p", { className: "message", attrs: { role: "status" } });
      check.addEventListener("click", () =>
        withBusy(check, async () => {
          const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(body.id)}/check`, { method: "POST" });
          if (!ok) {
            setMessage(status, out.error || t("could not check these programs"), "error");
            return;
          }
          record(t("Checked what {name}'s programs are", { name: body.name }),
            (out.compatibility || []).map((row) => `${row.executable}: ${row.identity || row.state}`).join(", "),
            out.readiness?.ready ? "ok" : "error");
          await refreshReadiness(body.id);
        })
      );
      section.append(el("div", { className: "row-actions", children: [check] }), status);
    }
    return section;
  }

  // refreshReadiness asks the server again after a binding was recorded and
  // replaces the state, the readiness box, the exact programs and the
  // commands in place — the same server-side decision as Build & Run's lists
  // (NEW_310, Q2_008). Nothing is worked out here.
  async function refreshReadiness(id) {
    const current = document.getElementById("profile-readiness");
    if (!current) return;
    const sequence = detailSequence;
    const { ok, body } = await api("/api/v1/profiles/" + encodeURIComponent(id));
    if (!ok || !body.readiness || sequence !== detailSequence || document.getElementById("profile-readiness") !== current) return;
    current.replaceWith(readinessBox(body));
    $("profile-state")?.lastChild?.replaceWith(stateBadge(body));
    document.getElementById("profile-compatibility")?.replaceWith(compatibilityBox(body));
    const previews = document.querySelector("#profile-details .profile-arguments__previews");
    if (previews) await refreshPreviews(id, previews);
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
  // actually are on this machine. Recorded separately from the profile and
  // never part of one that is exported. The heading keeps its words: the
  // checker's fixes send people "under Where these programs are on this
  // machine".
  function renderSetup(detail, body) {
    const executables = executableNames(body);
    if (executables.length === 0) return;

    detail.append(el("h5", { text: t("Where these programs are on this machine"), attrs: { id: "profile-setup" } }));

    // The Companion downloads no program (operator, 2026-09-23): the way to a
    // program nobody has installed yet is its own homepage — said only while
    // nothing is chosen.
    if (body.homepage && nothingChosenYet(body)) {
      const get = el("p", { className: "muted small" });
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
    const useFolder = el("button", { text: t("Use this folder"), attrs: { type: "button", class: "secondary", id: "profile-use-folder" } });
    const folderStatus = el("p", { className: "message", attrs: { role: "status" } });
    detail.append(folder.container, el("div", { className: "row-actions", children: [useFolder] }), folderStatus);

    // Each program on its own, under a disclosure when a folder can find them
    // all; open when one of them is already named, so what is recorded shows.
    const each = el("details", { className: "profile-programs", attrs: { id: "profile-programs" } });
    each.open = Object.values(body.binding?.executables || {}).some(Boolean) || executables.length === 1;
    each.append(el("summary", { text: t("Or choose each program") }));
    const fields = new Map();
    for (const executable of executables) {
      const field = window.AUCOM.pathField({
        id: "profile-exe-" + executable.name,
        kind: "open-file",
        label: `${executable.title || executable.name} (${executable.name})`,
        value: (body.binding?.executables || {})[executable.name] || "",
        hint: t("The profile looks for a file named {file}.", { file: window.AUCOM.programFileName(executable.file) }),
      });
      fields.set(executable.name, field);
      each.append(field.container);
    }
    detail.append(each);
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
        label: `${window.AUCOM.folderTitle(role)}${purposes.optional ? " " + t("(optional)") : ""}`,
        value: (body.binding?.roots || {})[role] || "",
        hint: purposes.length ? `Used to ${purposes.join("; ")}.` : "",
      });
      rootFields.set(role, field);
      detail.append(field.container);
    }

    const save = el("button", { text: t("Save these paths"), attrs: { type: "button", class: "primary" } });
    const saveStatus = el("p", { className: "message", attrs: { role: "status" } });

    // "Look for installed games" fills the game folder in from the usual
    // places (operator, 2026-09-23). A candidate only fills the field; Save
    // records it.
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
          setMessage(saveStatus, out.error || t("could not record these paths"), "error");
          return;
        }
        // What was RECORDED, which is not always what was typed: an engine's
        // project folder left empty is the Companion's own (NEW_330).
        const recorded = out.binding?.roots || {};
        const filled = rootFields.has("content_root") && !rootPaths.content_root && recorded.content_root;
        for (const [role, field] of rootFields) field.input.value = recorded[role] || "";
        setMessage(saveStatus, filled
          ? t("Recorded. The project folder is the Companion's own, where it keeps the maps and textures it downloads; change it above if you want another. It survives a restart.")
          : t("Recorded. It survives a restart."), "ok");
        record(`Recorded where ${body.name} is`, Object.values(paths).filter(Boolean).join(", "), "ok");
        await refreshReadiness(body.id);
      })
    );
    useFolder.addEventListener("click", () =>
      withBusy(useFolder, async () => {
        const chosen = folder.input.value.trim();
        if (!chosen) {
          setMessage(folderStatus, t("Choose the folder first, with Browse… or by typing it."), "error");
          folder.input.focus();
          return;
        }
        const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(body.id)}/bind`, {
          method: "POST",
          body: { folder: chosen },
        });
        if (!ok) {
          setMessage(folderStatus, out.error || t("could not use this folder"), "error");
          return;
        }
        for (const [name, field] of fields) field.input.value = (out.binding?.executables || {})[name] || "";
        setMessage(folderStatus, t("Found all {n} programs. Recorded; it survives a restart.", { n: fields.size }), "ok");
        record(`Recorded where ${body.name} is`, chosen, "ok");
        await refreshReadiness(body.id);
      })
    );
    detail.append(el("div", { className: "row-actions", children: [save] }), saveStatus);
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
    $("wizard-next").disabled = step === 4 || !templateReady;
    const kind = $("wizard-kind").value;
    $("wizard-engine-fields").hidden = kind !== "engine";
    $("wizard-tool-fields").hidden = kind !== "tool";
    $("wizard-homepage-field").hidden = kind === "pipeline";
    $("wizard-kind-hint").textContent = t(KIND_HINTS[kind] || "");
    $("wizard-step-3-title").textContent = kind === "pipeline" ? "Stages and their parameters" : "Programs and actions";
    $("wizard-step-3-note").textContent = kind === "pipeline"
      ? "A pipeline is where parameters are set. Add as many stages as you need, in the order they run; each one picks a tool, says where its inputs come from and carries its own parameters and arguments. The same tool may be a stage more than once."
      : kind === "tool"
        ? "A build tool says where its programs are and what each one can do. What they are run with is set in the pipelines that use them."
        : "The engine's programs, and what each action starts.";
    if (step === 3) window.AUCOM.scratch.render();
  }

  // One short line per kind, on the selection screen only.
  const KIND_HINTS = {
    tool: "A build tool: where compiler programs are and what each can do.",
    pipeline: "A build pipeline: the stages a build runs, in order, each with a tool and its options.",
    engine: "A game engine: the program that plays the map, and the game folder it reads.",
  };

  // pinnedTemplate is the template a `#new-profile/<id>` link asked for.
  let pinnedTemplate = "";

  // The starting points for the kind being described: From scratch first and
  // by default, then every tested profile of that kind (operator, 2026-10-03).
  async function refreshTemplates() {
    const kind = $("wizard-kind").value;
    templateReady = false;
    $("wizard-next").disabled = true;
    const requestID = ++templateRequest;
    const { ok, body } = await api("/api/v1/profiles/templates?kind=" + encodeURIComponent(kind));
    if (requestID !== templateRequest || kind !== $("wizard-kind").value) return;
    if (!ok) {
      // One truthful message and Retry; the draft and the list as they were
      // are kept, and Next waits, rather than a blank list that looks empty.
      $("wizard-template-error-text").textContent = t("The tested profiles could not be read: {reason}", { reason: body.error || t("no answer") }) + " ";
      $("wizard-template-error").hidden = false;
      $("wizard-next").disabled = true;
      return;
    }
    $("wizard-template-error").hidden = true;
    const select = $("wizard-template");
    select.replaceChildren();
    select.append(el("option", { text: "From scratch", attrs: { value: "" } }));
    templates = body.items || [];
    for (const template of templates) {
      // One written on this computer says so and says which version: nobody
      // tested it but whoever wrote it.
      select.append(el("option", { text: template.installed
        ? `${template.name} ${template.version} (installed here) — ${template.summary}`
        : `${template.name} — ${template.summary}`, attrs: { value: template.id } }));
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
    const generation = draftGeneration;
    templateReady = false;
    $("wizard-next").disabled = true;
    const requestID = ++templateRequest;
    if (!template) {
      scratch.clear(kind);
      for (const id of Object.keys(IDENTITY)) offer(id, ({ "wizard-version": "1.0.0", "wizard-publisher": "Local", "wizard-license": "NOASSERTION" })[id] || "");
      $("wizard-template-note").textContent =
        "Nothing is filled in: you write every field. Choose a tested profile above to have all of them filled, and change what is different about yours.";
      scratch.render();
      templateReady = true;
      $("wizard-next").disabled = step === 4;
      return;
    }
    const { ok, body } = await api(`/api/v1/profiles/templates/${encodeURIComponent(template.id)}/scratch`);
    if (requestID !== templateRequest || generation !== draftGeneration || kind !== $("wizard-kind").value || template.id !== $("wizard-template").value) return;
    if (!ok) {
      setMessage("wizard-message", body.error || "that profile could not be read", "error");
      return;
    }
    scratch.fill(body.scratch, body.stage_arguments || {});
    for (const [id, key] of Object.entries(IDENTITY)) offer(id, body.identity?.[key]);
    $("wizard-template-note").textContent =
      `Filled in from ${template.name} ${template.version}` + (template.summary ? ` — ${template.summary}` : "") +
      ". Every field in the next steps is yours to change, add to or remove; what the form has no field for is kept as " +
      (template.installed
        ? "installed. Keep its name to make a new version of it (change the version, and tick the replace box when you install); give it another name to make a separate profile."
        : "tested.");
    scratch.render();
    templateReady = true;
    $("wizard-next").disabled = step === 4;
  }

  function composeRequest(fromDocument) {
    if (fromDocument) {
      // Editing a document the user pasted: the form fields are not applied on
      // top of it, because they belong to a different document and would
      // silently rewrite what somebody pasted.
      return { document: fromDocument, template: $("wizard-template").value, stage_arguments: window.AUCOM.scratch.stageTokens() };
    }
    const kind = $("wizard-kind").value;
    const value = (id) => $(id).value.trim() || undefined;
    const request = {
      scratch: window.AUCOM.scratch.request(),
      name: value("wizard-name"), version: value("wizard-version"), summary: value("wizard-summary"),
      publisher_name: value("wizard-publisher"), license_spdx: value("wizard-license"),
      stage_arguments: window.AUCOM.scratch.stageTokens(),
    };
    if (kind !== "pipeline") request.homepage = value("wizard-homepage");
    if (kind === "engine") {
      request.runtime = value("wizard-runtime");
      request.engine_version = value("wizard-engine-version");
    }
    return request;
  }

  async function compose(fromDocument) {
    const generation = draftGeneration;
    const sequence = ++composeSequence;
    const key = draftKey();
    const tokens = structuredClone(window.AUCOM.scratch.stageTokens());
    composed = null;
    $("wizard-import").disabled = true;
    const { ok, body } = await api("/api/v1/profiles/compose", {
      method: "POST", body: composeRequest(fromDocument),
    });
    if (generation !== draftGeneration || sequence !== composeSequence || key !== draftKey()) return null;
    if (!ok || !body.valid) {
      setMessage("wizard-message", body.error || "The document could not be validated. Retry without losing your draft.", "error");
      const identityFields = { name: "wizard-name", version: "wizard-version", summary: "wizard-summary",
        "publisher.name": "wizard-publisher", "license.spdx": "wizard-license", runtime: "wizard-runtime",
        engine_version: "wizard-engine-version" };
      let first = null;
      for (const problem of body.problems || []) {
        const id = identityFields[problem.path];
        if (id) { setStep(2); first ||= fieldError(id, problem.message); }
        // A stage's own argument the stage already passes (NEW_360): refused
        // here, before anything is installed, and shown where it was typed.
        if (!first && problem.path.startsWith("stage_arguments.")) {
          setStep(3);
          window.AUCOM.scratch.stageProblem(problem.path.slice("stage_arguments.".length), problem.message);
          return null;
        }
      }
      (first || $("scratch-body").querySelector("input, select, textarea, button"))?.focus();
      return null;
    }
    composed = body;
    validatedKey = key;
    validatedStageTokens = tokens;
    $("wizard-json").value = JSON.stringify(body.document, null, 2);
    if (!fromDocument) formDocument = $("wizard-json").value;
    renderReview(body);
    setMessage("wizard-message", "");
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
        text: body.kind === "pipeline"
          ? "Installing this does not approve it. It will be listed as local — nobody has vouched for it, including you. " +
            "A pipeline starts nothing of its own: each stage runs a tool, and a tool runs only once it is approved and set up."
          : "Installing this does not approve it. It will be listed as local — nobody has vouched for it, " +
            "including you — and it cannot run until you have approved it.",
      })
    );

    const profileDocument = body.document;
    review.append(el("p", { text: `Kind: ${body.kind}. Publisher: ${profileDocument.publisher?.name || ""}. Licence: ${profileDocument.license?.spdx || ""}.` }));
    const declarations = el("ul", { className: "plain" });
    for (const program of profileDocument.executables || []) declarations.append(el("li", { text: `${program.name}: ${window.AUCOM.programFileName(program.file)}` }));
    for (const action of profileDocument.actions || []) declarations.append(el("li", { text: `${action.title || action.id} (${action.id}) → ${action.executable}` }));
    for (const stage of profileDocument.steps || []) declarations.append(el("li", { text: `${stage.title || stage.id} (${stage.id}) → ${stage.capability}` }));
    review.append(declarations);
    for (const stage of profileDocument.steps || []) {
      // A stage's wiring and parameters, as the document has them: what the
      // review is for, and what a tool change must not have emptied.
      const wires = (stage.inputs || []).map((wire) => `${wire.name} ← ${wire.from}`);
      const options = Object.entries(stage.options || {}).map(([name, value]) => `${name} = ${value}`);
      declarations.append(el("li", { className: "muted", text:
        `${stage.id}: ${stage.tool ? "run by " + stage.tool : "run by whichever installed tool provides it"}; ` +
        `inputs ${wires.length ? wires.join(", ") : "none wired"}; parameters ${options.length ? options.join(", ") : "all default"}` }));
    }
    review.append(setupSummary(body));

    if (body.diff && !body.diff.empty) {
      review.append(el("h4", { text: "What you changed from the profile you started from" }));
      if (body.diff.escalates) {
        review.append(
          el("p", {
            className: "message error",
            text: "This asks for more than the profile you started from did.",
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

  // What is left to do after Install, from the Companion's own readiness
  // answer for this document (composesetup.go) — the same one Profiles and
  // Build & Run read. Installed is not ready, and the review says which.
  function setupSummary(body) {
    const box = el("div", { className: "wizard-setup", attrs: { id: "wizard-setup" } });
    const setup = body.setup;
    if (!setup) {
      box.append(el("p", { className: "muted", text: "What this still needs could not be read. Its page in Profiles says, once it is installed." }));
      return box;
    }
    box.append(el("h4", { text: setup.ready
      ? (body.kind === "pipeline" ? "Every stage's tool is ready: this can build once installed" : "Ready to run once installed")
      : "Installing does not make it ready. Still to do:" }));
    const list = el("ul", { className: "plain" });
    // The checker names a missing approval itself for a tool or an engine;
    // it is said once.
    if (!setup.approved && !(setup.problems || []).some((problem) => problem.fault === "not_authorized")) {
      list.append(el("li", { text: body.kind === "pipeline"
        ? "Not approved yet. Its page in Profiles opens when you install it: read its stages there and approve it."
        : "Approve it. Nobody has yet, and nothing unapproved runs. Its page in Profiles opens when you install it; Approve is there." }));
    }
    for (const problem of setup.problems || []) {
      list.append(el("li", { text: [problem.summary, problem.fix].filter(Boolean).join(" ") }));
    }
    if (list.children.length) box.append(list);
    if (!setup.ready) {
      box.append(el("p", { className: "muted", text: body.kind === "pipeline"
        ? "A pipeline starts nothing of its own: it is ready when it is approved and every stage's tool is."
        : "Where each program is on this computer is chosen on its page in Profiles, never written in the profile." }));
    }
    return box;
  }

  $("area-new-profile").addEventListener("input", (event) => {
    if (event.target.id !== "wizard-replace") invalidateDraft();
  }, true);
  $("area-new-profile").addEventListener("change", (event) => {
    if (event.target.id !== "wizard-replace") invalidateDraft();
  }, true);
  $("scratch-body").addEventListener("click", (event) => {
    if (event.target.closest("button")) invalidateDraft();
  });
  // handEdited: the text of a document the person changed or opened by hand,
  // kept until it is checked, installed, or explicitly discarded.
  let handEdited = null;
  let formDocument = "";
  function documentEdited() {
    return handEdited !== null || ($("wizard-json").value !== "" && $("wizard-json").value !== formDocument);
  }
  $("wizard-json").addEventListener("input", () => { $("wizard-import").disabled = true; handEdited = $("wizard-json").value; });
  $("wizard-kind").addEventListener("change", async () => {
    invalidateDraft();
    // Only what depends on the kind is cleared: programs, actions and
    // stages. The name, the game and the other identity fields are kept.
    for (const kind of ["tool", "engine", "pipeline"]) window.AUCOM.scratch.clear(kind);
    await refreshTemplates();
    setMessage("wizard-message", t("Kind changed: programs and stages from the previous kind were cleared. Your name, game and other details are kept."));
    setStep(1);
  });
  $("wizard-template").addEventListener("change", () => { invalidateDraft(); applyTemplate(); });
  $("wizard-template-retry").addEventListener("click", (event) => withBusy(event.currentTarget, refreshTemplates));
  // Going back to the form from a document edited by hand: the form cannot
  // show every field such a document may have, and composing from the form
  // again would replace it. So it is asked, never done silently.
  $("wizard-back").addEventListener("click", async () => {
    if (installing) return;
    if (step === 4 && documentEdited()) {
      const discard = await window.AUCOM.confirmModal({
        title: t("Leave the edited document?"),
        body: t("Going back builds the document from the form again, and your edits to the document itself are dropped."),
        confirm: t("Discard document edits"), cancel: t("Keep editing"),
      });
      if (!discard) { $("wizard-json").focus(); return; }
      handEdited = null;
      $("wizard-json").value = formDocument;
    }
    setStep(step - 1);
  });
  $("wizard-next").addEventListener("click", async (event) => {
    if (installing || !templateReady || !validateStep()) return;
    const currentStep = step;
    if (currentStep === 3) {
      const result = await withBusy(event.currentTarget, () => compose(null));
      if (!result?.valid || step !== currentStep) return;
    }
    setStep(currentStep + 1);
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

  // Download: the browser saves the document where it saves downloads, under
  // the profile's own name. The Companion writes nothing outside its folders.
  $("wizard-download-json").addEventListener("click", () => {
    const text = $("wizard-json").value;
    if (!text.trim()) {
      setMessage("wizard-message", t("There is no document yet: complete the form first."), "error");
      return;
    }
    let name = "profile";
    try { name = JSON.parse(text).id || name; } catch { /* the text is saved as it is */ }
    const file = `${name}.json`;
    const link = el("a", { attrs: { href: URL.createObjectURL(new Blob([text], { type: "application/json" })), download: file } });
    link.click();
    setTimeout(() => URL.revokeObjectURL(link.href), 10000);
    setMessage("wizard-message", t("Your browser saves it as “{file}” where it keeps downloads.", { file }), "ok");
  });

  // Open: the person picks the file in the browser's own chooser and the page
  // reads that one file's text. Cancelling changes nothing and says so.
  $("wizard-load-json").addEventListener("click", () => $("wizard-load-file").click());
  $("wizard-load-file").addEventListener("cancel", () => {
    setMessage("wizard-message", t("No file was chosen; the document is unchanged."));
    $("wizard-load-json").focus();
  });
  $("wizard-load-file").addEventListener("change", async () => {
    const input = $("wizard-load-file");
    const file = input.files?.[0];
    input.value = "";
    if (!file) {
      setMessage("wizard-message", t("No file was chosen; the document is unchanged."));
      return;
    }
    const text = await file.text();
    $("wizard-json").value = text;
    handEdited = text;
    $("wizard-import").disabled = true;
    $("wizard-json-details").open = true;
    setMessage("wizard-message", t("Opened “{file}”. Press Check this document before installing it.", { file: file.name }));
    $("wizard-check-json").focus();
  });

  $("wizard-import").addEventListener("click", async (event) => {
    if (installing || (!pendingInstallation && (!composed?.valid || validatedKey !== draftKey() ||
        $("wizard-json").value !== JSON.stringify(composed.document, null, 2)))) return;
    installing = true;
    const controls = [...$("area-new-profile").querySelectorAll("button, input, select, textarea")];
    const disabled = controls.map((node) => node.disabled);
    controls.forEach((node) => node.disabled = true);
    try {
      if (!pendingInstallation) {
        const { ok, body } = await api("/api/v1/profiles/import", {
          method: "POST", body: { document: composed.document, replace: $("wizard-replace").checked },
        });
        if (!ok) {
          setMessage("wizard-message", body.error || "The profile could not be installed. Your draft is kept.", "error");
          record("Profile import refused", body.error, "failed");
          return;
        }
        pendingInstallation = { id: body.id, name: body.name, stages: structuredClone(validatedStageTokens) };
        record(`Installed the profile ${body.name || ""}`.trim(), "", "ok");
      }
      while (pendingInstallation.stages.length) {
        const item = pendingInstallation.stages[0];
        const saved = await api(`/api/v1/profiles/${encodeURIComponent(pendingInstallation.id)}/stage-arguments`, {
          method: "POST", body: item,
        });
        if (!saved.ok) {
          // Retrying the same tokens would be refused the same way (NEW_360).
          // The pipeline is installed, so correcting them is its own page's
          // job: it opens with the unsaved tokens filled in and the refusal
          // beside them. Nothing is installed twice.
          const { id, name, stages } = pendingInstallation;
          pendingInstallation = null;
          composed = null;
          record(`Installed the profile ${name || ""}, but stage ${item.stage} arguments were refused`.trim(), saved.body.error || "", "failed");
          await window.AUCOM.openInstalledProfile(id, { unsaved: stages, problem: { stage: item.stage, message: saved.body.error || "" } });
          return;
        }
        pendingInstallation.stages.shift();
      }
      const id = pendingInstallation.id;
      pendingInstallation = null;
      composed = null;
      await window.AUCOM.openInstalledProfile(id);
    } finally {
      installing = false;
      controls.forEach((node, index) => node.disabled = disabled[index]);
      $("wizard-import").disabled = !pendingInstallation && !composed?.valid;
      if (pendingInstallation) {
        controls.forEach((node) => { if (node !== $("wizard-import")) node.disabled = true; });
      }
    }
  });

  $("profiles-kind").addEventListener("change", refreshList);
  $("profiles-search").addEventListener("input", drawList);
  // Another account or another server: anything asked before it answers for
  // the old one, so it is dropped and the current view asked again (NEW_364
  // P7). Profiles are this machine's, so the answer is usually the same — the
  // fence is what makes "usually" irrelevant.
  let accountKey = null;
  document.addEventListener("aucom:status", () => {
    const status = window.AUCOM.status || {};
    const key = `${status.aub_base_url || ""}|${status.email || ""}|${Boolean(status.authenticated)}`;
    if (accountKey !== null && key !== accountKey) {
      viewGeneration += 1;
      if (!$("area-profiles").hidden) {
        const match = window.location.hash.match(/^#profiles\/(.+)$/);
        if (match) openProfile(decodeURIComponent(match[1])); else refreshList();
      }
    }
    accountKey = key;
  });
  $("profile-breadcrumb-up").addEventListener("click", (event) => {
    event.preventDefault();
    window.AUCOM.showArea("profiles");
  });

  // New profile opens both ways of writing one — from a tested template, or
  // from scratch — at the top of the area, where the button is.
  // After an install on the New profile page, this tab becomes Profiles with
  // the new document's review open: approving it and saying where its programs
  // are is the next thing to do, and both live there.
  window.AUCOM.openInstalledProfile = async (id, handoff = null) => {
    unsavedStages = handoff ? { id, ...handoff } : null;
    document.body.classList.remove("single-view");
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
        if (found) {
          $("wizard-kind").value = found.kind;
          $("wizard-kind").dispatchEvent(new Event("aucom:sync"));
        }
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
        lastOpened = decodeURIComponent(argument);
        await openProfile(lastOpened);
        window.scrollTo({ top: 0 });
        return;
      }
      detailSequence += 1;
      await refreshList();
      // Back from a profile's page, focus returns to its row.
      if (lastOpened) {
        document.querySelector(`#profiles-list [data-profile="${CSS.escape(lastOpened)}"] a`)?.focus();
        lastOpened = "";
      }
    },
  };
})();
