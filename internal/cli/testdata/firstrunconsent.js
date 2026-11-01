// The first-run question in the real page of a real `serve` (NEW_374 L8):
// answers the Install window the way the test says and reports what it saw.

"use strict";

(async () => {
  const report = { title: "", links: [], notice: "", error: "" };
  const waitFor = async (what, predicate, timeout = 45000) => {
    const deadline = Date.now() + timeout;
    for (;;) {
      const value = predicate();
      if (value) return value;
      if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  };
  const dialogNamed = (title) => [...document.querySelectorAll("dialog[open]")]
    .find((dialog) => dialog.querySelector("h2")?.textContent === title);
  const press = (dialog, label) => [...dialog.querySelectorAll("button")].find((button) => button.textContent === label).click();

  try {
    const { answer } = await (await fetch("/journey/config")).json();
    const window_ = await waitFor("the Install window", () => dialogNamed("Install the Auto-Pigeon build tools"));
    report.title = window_.querySelector("h2").textContent;
    report.links = [...window_.querySelectorAll("a")].map((link) => link.textContent);
    press(window_, answer);
    if (answer === "Install") {
      const notice = await waitFor("the installed notice", () => dialogNamed("Build tools installed"));
      report.notice = notice.querySelector("code")?.textContent || "";
      press(notice, "OK");
    }
  } catch (error) {
    report.error = error.message;
  }
  await fetch("/journey/report", { method: "POST", body: JSON.stringify(report) });
})();
