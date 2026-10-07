// NEW_310A: when the Companion stops, every other dialog goes with it. Found
// live on Windows: the leak-test pipeline chooser stayed open and the
// "has stopped" notice was drawn around it.
//
// Runs lifecycle.js itself against a fake page and a fake lease socket.

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../assets/lifecycle.js", import.meta.url), "utf8");

const elements = {};
const node = (id, className = "") => (elements[id] ||= {
  id, className, hidden: true, textContent: "", open: false, listeners: {},
  addEventListener(type, fn) { this.listeners[type] = fn; }, focus() {}, close() { this.open = false; },
});
const modals = ["stopped-modal", "quit-modal", "leak-pin-modal", "sign-in-modal"].map((id) => node(id, "auth-modal"));
node("leak-pin-modal").hidden = false; // the chooser is open
const dialog = node("report-dialog");
dialog.open = true; // and a native <dialog> too

let socket;
class FakeSocket {
  static OPEN = 1;
  constructor() { this.listeners = {}; this.readyState = 1; socket = this; }
  addEventListener(type, fn) { this.listeners[type] = fn; }
  send() {}
  close() {}
}
const document = {
  title: "Companion",
  querySelector: () => node("dismiss"),
  querySelectorAll(selector) {
    if (selector === ".auth-modal") return modals;
    if (selector === "dialog[open]") return [dialog].filter((d) => d.open);
    return [];
  },
};
const window = {
  location: { protocol: "http:", host: "127.0.0.1:8789" },
  setTimeout() {}, setInterval() {},
  AUCOM: {
    $: (id) => node(id), el: () => node("x"), api: async () => ({ status: 200, body: {} }),
    setMessage() {}, withBusy: (_c, fn) => fn(), record() {}, t: (m) => m,
  },
};
new Function("window", "document", "WebSocket", "apiToken", source)(window, document, FakeSocket, "token");

socket.listeners.message({ data: JSON.stringify({ type: "stopping", cause: "quit" }) });
socket.listeners.close();

assert.equal(node("stopped-modal").hidden, false, "the stopped notice is not shown");
for (const id of ["leak-pin-modal", "quit-modal", "sign-in-modal"]) {
  assert.equal(node(id).hidden, true, `${id} stayed open around the stopped notice`);
}
assert.equal(dialog.open, false, "an open <dialog> stayed open around the stopped notice");
assert.match(document.title, /Stopped/);
console.log("lifecycle: the stopped notice closes every other dialog — ok");
