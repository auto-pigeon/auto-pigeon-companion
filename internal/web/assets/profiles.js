// The Profiles area: reading a profile, writing one, and approving one.
//
// The wizard never builds JSON. It posts the fields somebody filled in and the
// Companion composes, validates and digests the document — so what the advanced
// view shows is the real document rather than a prediction of one, and the
// browser is not a second implementation of a schema it would eventually
// disagree with.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, record, badge, when, permissionBlock,
    maturityBadge, maturityNote, openCompatibilityReport } = window.AUCOM;

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
    setMessage("profiles-message", `${installed.length} profile(s) on this machine.`);
    for (const profile of installed) list.append(profileCard(profile));
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
    const identity = el("p", { className: "muted profile-card__version", text: `Version ${profile.version}` });

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
    open.addEventListener("click", () => {
      if (needsReview) {
        openProfile(profile.id);
        return;
      }
      openConfigureTab(profile.id);
    });

    return el("li", {
      className: "card profile-card",
      children: [title, marks, summary, identity, el("div", { className: "row-actions", children: [open] })],
    });
  }

  // configureURL is one profile's configuration page: a URL that survives a
  // reload and names the profile and nothing else. The id is the only thing in
  // it — no API token (the server puts that in the page it renders, so a fresh
  // tab gets its own) and no local path, because a URL is copied, logged and
  // pasted, and neither belongs anywhere that happens. `?view=` drops the area
  // navigation the same way the New profile tab already does; the `#profiles/<id>`
  // hash is what `app.js` hands back to `refresh(argument)` on load.
  function configureURL(id) {
    return window.location.pathname + "?view=profile#profiles/" + encodeURIComponent(id);
  }

  function openConfigureTab(id) {
    window.open(configureURL(id), "_blank", "noopener");
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

    detail.append(el("h4", { text: "What it asks to be allowed to do" }));
    if ((body.permissions || []).length === 0) {
      detail.append(el("p", { className: "muted", text: "Nothing. This document declares no permissions." }));
    } else {
      for (const permission of body.permissions) {
        detail.append(permissionBlock(permission));
      }
    }

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
      status.textContent = `Approved on ${when(body.binding?.granted_at)}.`;
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
      status.textContent = body.authorization_error || "This profile has not been approved on this machine.";
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

    const exportButton = el("button", { text: "Export the document", attrs: { type: "button", class: "secondary" } });
    exportButton.addEventListener("click", () =>
      withBusy(exportButton, async () => {
        const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(id)}/document`);
        if (!ok) {
          setMessage(status, out.error, "error");
          return;
        }
        $("wizard-json").value = JSON.stringify(out.document, null, 2);
        $("wizard-json-details").open = true;
        setStep(4);
        $("wizard-json").focus();
        setMessage(status, "Copied into the advanced view at the bottom of this page.", "ok");
      })
    );
    buttons.append(exportButton);

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

    renderSetup(detail, body);
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
      } else if (acquisition === "managed_download") {
        provenance.append(badge("verified download", "verified"),
          document.createTextNode(" Downloaded and checked against the signed Auto-Pigeon catalogue."));
      } else {
        provenance.append(badge("local binding", "local"),
          document.createTextNode(
            " Programs you named on this machine. Nothing has checked these files against a catalogue; " +
            "the profile's builtin badge is about the document, not about these bytes."));
      }
    };
    describeProvenance(body.binding);
    detail.append(provenance);

    const folder = window.AUCOM.pathField({
      id: "profile-folder",
      kind: "directory",
      label: `The folder that holds ${body.name}`,
      hint: "For ericw-tools, the folder you unpacked the release into (the one containing bin).",
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
        hint: `The profile looks for a file named ${window.AUCOM.programFileName(executable.file)}.`,
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
    $("wizard-engine-fields").hidden = $("wizard-kind").value !== "engine";
  }

  async function refreshTemplates() {
    const kind = $("wizard-kind").value;
    const { ok, body } = await api("/api/v1/profiles/templates?kind=" + encodeURIComponent(kind));
    const select = $("wizard-template");
    select.replaceChildren();
    if (!ok) {
      setMessage("wizard-message", body.error, "error");
      return;
    }
    templates = body.items || [];
    for (const template of templates) {
      select.append(el("option", { text: `${template.name} — ${template.summary}`, attrs: { value: template.id } }));
    }
    describeTemplate();
  }

  function currentTemplate() {
    return templates.find((template) => template.id === $("wizard-template").value);
  }

  function describeTemplate() {
    const template = currentTemplate();
    if (!template) {
      $("wizard-template-note").textContent = "No template of that kind is built in.";
      return;
    }
    $("wizard-template-note").textContent =
      `Version ${template.version}` + (template.summary ? ` · ${template.summary}` : "");
    renderTemplateFields(template);
  }

  function renderTemplateFields(template) {
    const executables = $("wizard-executables");
    executables.replaceChildren();
    for (const executable of template.executables || []) {
      const id = "wizard-exe-" + executable.name;
      executables.append(
        el("div", {
          className: "field grow",
          children: [
            el("label", { text: `File name for "${executable.title || executable.name}"`, attrs: { for: id } }),
            el("input", { attrs: { id, type: "text", value: executable.file, spellcheck: "false" } }),
            el("span", {
              className: "hint",
              text: "The name of the program on disk. {platform.exe_suffix} becomes .exe on Windows and nothing elsewhere.",
            }),
          ],
        })
      );
    }
    const actions = $("wizard-actions");
    actions.replaceChildren();
    for (const action of template.actions || []) {
      const id = "wizard-action-" + action;
      const label = el("label", { className: "check" });
      const box = el("input", { attrs: { type: "checkbox", id, value: action, checked: "checked" } });
      label.append(box);
      label.append(document.createTextNode(" " + action));
      actions.append(label);
    }
    if (!$("wizard-runtime").value && template.runtime) $("wizard-runtime").value = template.runtime;
    if (!$("wizard-engine-version").value && template.engine_version) {
      $("wizard-engine-version").value = template.engine_version;
    }
  }

  function composeRequest(fromDocument) {
    const template = currentTemplate();
    const executables = {};
    for (const executable of template?.executables || []) {
      const value = $("wizard-exe-" + executable.name)?.value.trim();
      if (value) executables[executable.name] = value;
    }
    const actions = [...$("wizard-actions").querySelectorAll("input[type=checkbox]")]
      .filter((box) => box.checked)
      .map((box) => box.value);

    const request = {
      template: $("wizard-template").value,
      name: $("wizard-name").value.trim() || undefined,
      version: $("wizard-version").value.trim() || undefined,
      summary: $("wizard-summary").value.trim() || undefined,
      publisher_name: $("wizard-publisher").value.trim() || undefined,
      license_spdx: $("wizard-license").value.trim() || undefined,
      executables: Object.keys(executables).length ? executables : undefined,
      actions: actions.length ? actions : undefined,
    };
    if ($("wizard-kind").value === "engine") {
      request.runtime = $("wizard-runtime").value.trim() || undefined;
      request.engine_version = $("wizard-engine-version").value.trim() || undefined;
    }
    if (fromDocument) {
      request.document = fromDocument;
      // Editing a document the user pasted: the form fields are not applied on
      // top of it, because they belong to a different document and would
      // silently rewrite what somebody pasted.
      for (const key of Object.keys(request)) {
        if (key !== "document" && key !== "template") delete request[key];
      }
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
          "including you — and it cannot run until you have read what it asks for and approved it.",
      })
    );

    review.append(el("h4", { text: "What it asks to be allowed to do" }));
    if ((body.permissions || []).length === 0) {
      review.append(el("p", { className: "muted", text: "Nothing." }));
    } else {
      for (const permission of body.permissions) {
        review.append(permissionBlock(permission));
      }
    }

    if (body.diff && !body.diff.empty) {
      review.append(el("h4", { text: "What you changed from the template" }));
      if (body.diff.escalates) {
        review.append(
          el("p", {
            className: "message error",
            text: "This asks for more than the template did.",
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

  $("wizard-kind").addEventListener("change", () => {
    refreshTemplates();
    setStep(step);
  });
  $("wizard-template").addEventListener("change", describeTemplate);
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
      await window.AUCOM.openInstalledProfile(body.id);
    })
  );

  $("profiles-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshList));
  $("profiles-kind").addEventListener("change", refreshList);
  $("profile-detail-close").addEventListener("click", () => {
    $("profile-detail-panel").hidden = true;
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
    await openProfile(id);
    $("profile-detail-panel").scrollIntoView({ block: "start" });
  };

  // New profile opens the creator in a new browser tab: the page served there
  // carries this run's own token, so it is the same Companion, and the
  // installed list stays where the person left it (NEW_244D, operator).
  $("profiles-new").addEventListener("click", () => {
    window.open(window.location.pathname + "?view=new-profile#new-profile", "_blank", "noopener");
  });

  window.AUCOM.areas["new-profile"] = {
    async refresh() {
      if (templates.length === 0) await refreshTemplates();
      setStep(step);
      await window.AUCOM.scratch?.refresh?.();
    },
  };

  window.AUCOM.areas.profiles = {
    open: openProfile,
    configureURL,
    async refresh(argument) {
      await refreshList();
      // `#profiles/<id>` is one profile's configuration page, so a reload comes
      // back to that profile rather than to the list — the shape `#games/<id>`
      // already established. The list is refreshed first so the page behind the
      // detail is the real one if the user leaves single-view.
      if (argument) await openProfile(argument);
    },
  };
})();
