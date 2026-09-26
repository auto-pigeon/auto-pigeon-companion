// Driven by bugreport_model_test.go: exercises the page's bug-report model —
// the very module the dialog imports — and prints what it observed as JSON.
// The Go test asserts on it against bug-report-rules.json, which it reads
// itself, so the expectations are the contract's data and not a copy here.
//
// stdin: { "model": "<file URL of assets/bugreport-model.mjs>",
//          "contract": "<file URL of the vendored index.mjs>",
//          "incidents": [<GET /api/v1/bug-reports/incidents entries, from Go>] }

let input = "";
process.stdin.on("data", (chunk) => (input += chunk));
process.stdin.on("end", async () => {
  const { model: modelURL, contract: contractURL, incidents } = JSON.parse(input);
  const model = await import(modelURL);
  const contract = await import(contractURL);
  const {
    APPLICATION, DEFAULT_REPORT_TYPE, ROUTE_LINES, areaChoices, buildReport, dialogTitle, fieldHints, fieldLabels,
    initialClassification, missingChoices, reportLabels, reportTypeChoices, reviewBlocked, serverRoute, suggestedArea,
  } = model;
  const { prefilledIssueUrl, renderIssue, renderReportText, reportJsonDownload, BUG_REPORT_LIMITS } = contract;

  const fixed = {
    reportId: "0123456789abcdef0123456789abcdef",
    release: "1.150",
    environment: "production",
    now: Date.parse("2026-09-24T10:00:00.000Z"),
    client: { userAgent: "Mozilla/5.0 (X11; Linux x86_64) Chrome/140.0.0.0 Safari/537.36", language: "en-US" },
  };
  const fields = {
    summary: "Build stops at vis",
    steps: "Run the pipeline with token ghp_abcdefghijklmnopqrstuvwxyz0123 from /home/alice/maps",
    expected: "A map",
    actual: "An error at https://example.test/x?token=zzz",
  };
  const out = {
    application: APPLICATION,
    types: reportTypeChoices(),
    areas: areaChoices(),
    cold: initialClassification(null),
    coldMissing: missingChoices(initialClassification(null)),
    coldBuild: buildReport({ ...fixed, ...initialClassification(null), fields }),
    headings: { bug: fieldLabels("bug"), feature_request: fieldLabels("feature_request"), none: fieldLabels("") },
    every: [],
    incidents: [],
    foreign: [],
    // NEW_253: the dialog's DOM-free decisions.
    defaultType: DEFAULT_REPORT_TYPE,
    hints: { bug: fieldHints("bug"), feature_request: fieldHints("feature_request"), none: fieldHints("") },
    titles: {
      compose: { bug: dialogTitle("compose", "bug"), feature_request: dialogTitle("compose", "feature_request"), none: dialogTitle("compose", "") },
      review: { bug: dialogTitle("review", "bug"), feature_request: dialogTitle("review", "feature_request") },
    },
    blocked: {
      fresh: reviewBlocked({ ...initialClassification(null), summary: "" }),
      untyped: reviewBlocked({ reportType: "", area: "", summary: "x" }),
      typeOnly: reviewBlocked({ reportType: "", area: "other", summary: "x" }),
      areaMissing: reviewBlocked({ reportType: "feature_request", area: "", summary: "x" }),
      foreignArea: reviewBlocked({ reportType: "bug", area: "editor", summary: "x" }),
      noSummary: reviewBlocked({ reportType: "bug", area: "other", summary: "   " }),
      ready: reviewBlocked({ reportType: "feature_request", area: "other", summary: "x" }),
    },
    routeLines: ROUTE_LINES,
    routes: [],
    suggestedCold: suggestedArea(null),
  };
  for (const route of ["checking", "available", "unavailable", "unknown"]) {
    for (const authenticated of [false, true]) {
      for (const final of [false, true]) {
        out.routes.push({ route, authenticated, final, ...serverRoute({ route, authenticated, final }) });
      }
    }
  }

  // A deliberate Feature request, reviewed, taken back to Edit and reviewed
  // again: the draft (type, area, words, id, time) is what the dialog keeps,
  // and rebuilding from it is the same report — a feature request.
  {
    const draft = { ...initialClassification(null) };
    draft.reportType = "feature_request";
    draft.area = "documentation";
    const first = buildReport({ ...fixed, ...draft, fields });
    const again = buildReport({ ...fixed, ...draft, fields });
    out.roundTrip = [first, again].map((built) => (built.ok
      ? { reportType: built.document.report_type, reportId: built.document.report_id, labels: reportLabels(built.document), text: renderReportText(built.document) }
      : { errors: built.errors }));
  }

  // Every type x every offered area: the document, its labels and its four renderings.
  for (const type of reportTypeChoices()) {
    for (const area of areaChoices()) {
      const built = buildReport({ ...fixed, reportType: type.id, area: area.id, fields });
      if (!built.ok) { out.every.push({ type: type.id, area: area.id, errors: built.errors }); continue; }
      const d = built.document;
      const url = new URL(prefilledIssueUrl(d));
      const text = renderReportText(d);
      out.every.push({
        type: type.id,
        area: area.id,
        component: d.component,
        kind: d.kind,
        labels: reportLabels(d),
        text,
        json: reportJsonDownload(d),
        document: d,
        fits: built.prefill.fits,
        urlLength: prefilledIssueUrl(d).length,
        prefillLimit: BUG_REPORT_LIMITS.prefill_url,
        urlLabels: url.searchParams.get("labels"),
        urlBody: url.searchParams.get("body"),
        urlTitle: url.searchParams.get("title"),
        urlTemplate: url.searchParams.get("template"),
        prefilledBody: renderIssue(d, { route: "prefilled" }).body,
        serverBody: renderIssue(d, { route: "server" }).body,
      });
    }
  }

  // The incidents Go raised, as the page receives them: where each starts,
  // and the correction the person can make before Review.
  for (const incident of incidents) {
    const start = initialClassification(incident);
    const suggested = buildReport({ ...fixed, ...start, fields, incident });
    const corrected = buildReport({ ...fixed, reportType: "feature_request", area: "documentation", fields, incident });
    out.incidents.push({
      code: incident.code,
      start,
      suggestedArea: suggestedArea(incident),
      suggested: suggested.ok ? { kind: suggested.document.kind, labels: reportLabels(suggested.document), incident: suggested.document.incident, correlation: suggested.document.correlation_id } : { errors: suggested.errors },
      corrected: corrected.ok ? { kind: corrected.document.kind, labels: reportLabels(corrected.document), text: renderReportText(corrected.document) } : { errors: corrected.errors },
    });
  }

  // Areas other applications offer, and nonsense: the Companion's report refuses them.
  for (const area of ["editor", "gallery", "transform", "account_access", "Area: Companion", "bogus"]) {
    const built = buildReport({ ...fixed, reportType: "bug", area, fields });
    out.foreign.push({ area, ok: built.ok, errors: built.ok ? [] : built.errors });
  }
  const badType = buildReport({ ...fixed, reportType: "Bug", area: "companion", fields });
  out.badType = badType.ok ? [] : badType.errors;

  process.stdout.write(JSON.stringify(out));
});
