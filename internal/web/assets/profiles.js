// The Profiles area: reading a profile, writing one, and approving one.
//
// The wizard never builds JSON. It posts the fields somebody filled in and the
// Companion composes, validates and digests the document — so what the advanced
// view shows is the real document rather than a prediction of one, and the
// browser is not a second implementation of a schema it would eventually
// disagree with.

"use strict";

(() => {
  const { $, el, api, setMessage, busy, withBusy, record, badge, shortDigest, when,
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
    const title = el("h4", { text: profile.name });
    const marks = el("p");
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

    const summary = el("p", { className: "muted", text: profile.summary || "" });
    const identity = el("p", { className: "mono", text: `${profile.id} ${profile.version} · ${shortDigest(profile.digest)}` });

    const open = el("button", { text: "Review", attrs: { type: "button" } });
    open.addEventListener("click", () => openProfile(profile.id));

    return el("li", {
      className: "card",
      children: [title, marks, summary, identity, el("div", { className: "row-actions", children: [open] })],
    });
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
        about: `About ${body.name} (${body.id}).`,
        profiles: [{ role: body.kind === "engine" ? "engine" : body.kind, id: body.id, version: body.version }],
      })
    );
    if (note) detail.append(note);
    detail.append(el("p", { text: body.summary || "" }));
    if (body.description) detail.append(el("p", { className: "muted", text: body.description }));
    detail.append(
      el("p", {
        className: "mono",
        text: `${body.id} ${body.version} · ${body.digest} · from ${body.source}`,
      })
    );

    detail.append(el("h4", { text: "What it asks to be allowed to do" }));
    if ((body.permissions || []).length === 0) {
      detail.append(el("p", { className: "muted", text: "Nothing. This document declares no permissions." }));
    } else {
      for (const permission of body.permissions) {
        detail.append(
          el("div", {
            className: "permission",
            children: [
              el("p", { children: [el("strong", { text: permission.id }), document.createTextNode(" " + (permission.title || ""))] }),
              el("p", { className: "fix", text: permission.reason || permission.description || "" }),
            ],
          })
        );
      }
    }
    detail.append(el("pre", { className: "output", text: body.report || "" }));

    detail.append(el("h4", { text: "Actions" }));
    const actions = el("ul");
    for (const action of body.actions || []) {
      actions.append(
        el("li", {
          text: `${action.title || action.id} (${action.id})` +
            (action.capability ? ` — provides ${action.capability}` : "") +
            (action.session_role ? ` — ${action.session_role}` : ""),
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
      status.textContent = `Approved on ${when(body.binding?.granted_at)}, against ${shortDigest(body.binding?.profile_digest)}.`;
      status.className = "message ok";
      const withdraw = el("button", { text: "Withdraw approval", attrs: { type: "button", class: "danger" } });
      withdraw.addEventListener("click", () =>
        withBusy(withdraw, async () => {
          const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(id)}/withdraw`, { method: "POST" });
          if (!ok) {
            setMessage(status, out.error, "error");
            return;
          }
          record(`Withdrew approval for ${id}`, "", "cancelled");
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
          record(`Approved ${id}`, body.digest, "ok");
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

    detail.append(el("h4", { text: "Where these programs are on this machine" }));
    detail.append(
      el("p", {
        className: "muted",
        text:
          "This is recorded separately from the profile and is never part of a profile you export. " +
          "A managed download fills it in for you; a copy you installed yourself is named here.",
      })
    );
    const fields = new Map();
    for (const executable of executables) {
      const field = window.AUCOM.pathField({
        id: "profile-exe-" + executable.name,
        kind: "open-file",
        label: `${executable.title || executable.name}`,
        value: (body.binding?.executables || {})[executable.name] || "",
        hint: `The profile looks for a file named ${executable.file}.`,
      });
      fields.set(executable.name, field);
      detail.append(field.container);
    }
    const save = el("button", { text: "Save these paths", attrs: { type: "button", class: "primary" } });
    const saveStatus = el("p", { className: "message", attrs: { role: "status" } });
    save.addEventListener("click", () =>
      withBusy(save, async () => {
        const paths = {};
        for (const [name, field] of fields) paths[name] = field.input.value.trim();
        const { ok, body: out } = await api(`/api/v1/profiles/${encodeURIComponent(body.id)}/bind`, {
          method: "POST",
          body: { executables: paths },
        });
        if (!ok) {
          setMessage(saveStatus, out.error || "could not record these paths", "error");
          return;
        }
        setMessage(saveStatus, "Recorded. It survives a restart.", "ok");
        record(`Recorded where ${body.name} is`, Object.values(paths).filter(Boolean).join(", "), "ok");
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
      `${template.id} ${template.version} · actions: ${template.actions.join(", ")}` +
      (template.capabilities.length ? ` · provides: ${template.capabilities.join(", ")}` : "");
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
      id: $("wizard-id").value.trim() || undefined,
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
    review.append(el("p", { className: "mono", text: `${body.id} · ${body.digest}` }));
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
        review.append(
          el("div", {
            className: "permission",
            children: [
              el("p", { children: [el("strong", { text: permission.id })] }),
              el("p", { className: "fix", text: permission.reason || permission.description || "" }),
            ],
          })
        );
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
        `Installed as ${body.id}. It is listed above as ${body.trust} and is not approved yet.`,
        "ok"
      );
      record(`Installed the profile ${body.id}`, body.imported_to, "ok");
      await refreshList();
      await openProfile(body.id);
    })
  );

  $("profiles-refresh").addEventListener("click", (event) => withBusy(event.currentTarget, refreshList));
  $("profiles-kind").addEventListener("change", refreshList);
  $("profile-detail-close").addEventListener("click", () => {
    $("profile-detail-panel").hidden = true;
  });

  window.AUCOM.areas.profiles = {
    open: openProfile,
    async refresh() {
      await refreshList();
      if (templates.length === 0) await refreshTemplates();
      setStep(step);
    },
  };
})();
