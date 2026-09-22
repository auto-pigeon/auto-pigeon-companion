// Translations. English is the language the page is written in; every other
// language is a dictionary from the English text to its translation.
//
// Two paths, one dictionary:
//
//   - `t(english, values)` for a sentence script builds, with `{name}` slots.
//     The English is the key, so an untranslated string is simply shown in
//     English — never as a key nobody can read.
//   - Everything else is translated where it is shown: the page's own markup
//     at start-up, and every node script adds afterwards, by one observer. A
//     text node or a label attribute whose whole (trimmed) text is a key is
//     replaced by its translation. That is what lets the whole application be
//     translated without every `textContent =` in it being rewritten, and it
//     is why a key is the exact English sentence.
//
// The language is chosen in the header (the language menu) and kept in the
// Companion's own settings, not in browser storage: the page stores nothing
// of its own (TestThePageStoresNothingItShouldNot).

"use strict";

(() => {
  // The languages AUP offers, in AUP's order. English is the source; Italian
  // is translated; the rest are listed so a translator has somewhere to start,
  // and show English until they are.
  const LANGUAGES = [
    { code: "en", label: "English", translated: true },
    { code: "it", label: "Italiano", translated: true },
    { code: "fr", label: "Français", translated: false },
    { code: "de", label: "Deutsch", translated: false },
    { code: "es", label: "Español", translated: false },
    { code: "ja", label: "日本語", translated: false },
    { code: "zh", label: "中文", translated: false },
  ];

  const dictionaries = (window.AUCOM_LOCALES = window.AUCOM_LOCALES || {});
  let language = "en";
  let dictionary = null;

  const normalize = (text) => String(text).replace(/\s+/g, " ").trim();

  function lookup(english) {
    if (!dictionary) return null;
    const hit = dictionary[normalize(english)];
    return typeof hit === "string" && hit ? hit : null;
  }

  function fill(text, values) {
    if (!values) return text;
    return text.replace(/\{(\w+)\}/g, (whole, name) =>
      Object.prototype.hasOwnProperty.call(values, name) ? String(values[name]) : whole);
  }

  function t(english, values) {
    return fill(lookup(english) || english, values);
  }

  // --- translating what is on the page ----------------------------------------

  // The English each translated node started from, so switching language (or
  // back to English) re-translates from the source rather than from a
  // translation.
  const originalText = new WeakMap();
  const originalAttrs = new WeakMap();
  const ATTRIBUTES = ["placeholder", "aria-label", "title", "alt"];
  // Code, paths and commands are never translated, whatever they happen to say.
  const SKIP = "script, style, code, pre, kbd, samp, .mono, .no-i18n, [translate='no']";

  let applying = false;

  function translateText(node) {
    const parent = node.parentElement;
    if (!parent || parent.closest(SKIP)) return;
    let source = originalText.get(node);
    const current = node.nodeValue;
    // Script wrote something new into a node we translated before: that new
    // text is the source now.
    if (source === undefined || (current !== source.translated && current !== source.english)) {
      source = { english: current, translated: current };
    }
    const hit = language === "en" ? null : lookup(source.english);
    if (!hit) {
      if (current !== source.english) node.nodeValue = source.english;
      originalText.set(node, { english: source.english, translated: source.english });
      return;
    }
    // Keep the whitespace the markup had round the sentence.
    const lead = source.english.match(/^\s*/)[0];
    const trail = source.english.match(/\s*$/)[0];
    const translated = lead + hit + trail;
    if (current !== translated) node.nodeValue = translated;
    originalText.set(node, { english: source.english, translated });
  }

  function translateAttributes(element) {
    if (element.closest(SKIP)) return;
    let sources = originalAttrs.get(element);
    for (const name of ATTRIBUTES) {
      if (!element.hasAttribute(name)) continue;
      const current = element.getAttribute(name);
      sources = sources || {};
      const known = sources[name];
      if (!known || (current !== known.translated && current !== known.english)) {
        sources[name] = { english: current, translated: current };
      }
      const hit = language === "en" ? null : lookup(sources[name].english);
      const next = hit || sources[name].english;
      if (current !== next) element.setAttribute(name, next);
      sources[name].translated = next;
    }
    if (sources) originalAttrs.set(element, sources);
  }

  function translateTree(root) {
    if (!root) return;
    if (root.nodeType === Node.TEXT_NODE) {
      if (normalize(root.nodeValue)) translateText(root);
      return;
    }
    if (root.nodeType !== Node.ELEMENT_NODE && root.nodeType !== Node.DOCUMENT_NODE) return;
    if (root.nodeType === Node.ELEMENT_NODE) translateAttributes(root);
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
    let node = walker.nextNode();
    while (node) {
      if (node.nodeType === Node.TEXT_NODE) {
        if (normalize(node.nodeValue)) translateText(node);
      } else {
        translateAttributes(node);
      }
      node = walker.nextNode();
    }
  }

  function applyAll() {
    applying = true;
    try {
      translateTree(document.body);
      document.title = t("Auto-Pigeon Companion");
      document.documentElement.lang = language;
    } finally {
      applying = false;
    }
  }

  const observer = new MutationObserver((mutations) => {
    if (applying || language === "en") return;
    applying = true;
    try {
      for (const mutation of mutations) {
        if (mutation.type === "childList") {
          for (const node of mutation.addedNodes) translateTree(node);
        } else if (mutation.type === "characterData") {
          translateText(mutation.target);
        } else if (mutation.type === "attributes") {
          translateAttributes(mutation.target);
        }
      }
    } finally {
      applying = false;
    }
  });

  function setLanguage(code) {
    const known = LANGUAGES.some((item) => item.code === code);
    language = known ? code : "en";
    dictionary = language === "en" ? null : dictionaries[language] || {};
    applyAll();
    document.dispatchEvent(new CustomEvent("aucom:language", { detail: { language } }));
  }

  // The language "Automatic" means: the browser's own, when it is one that is
  // actually translated (AUP's rule — a French browser gets English until
  // French exists, though French can still be picked by hand).
  function preferredLanguage() {
    for (const tag of navigator.languages || [navigator.language || "en"]) {
      const code = String(tag).toLowerCase().split("-")[0];
      if (LANGUAGES.some((item) => item.code === code && item.translated)) return code;
    }
    return "en";
  }

  function start() {
    observer.observe(document.body, {
      subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ATTRIBUTES,
    });
  }

  if (document.body) start();
  else document.addEventListener("DOMContentLoaded", start, { once: true });

  window.AUCOM_I18N = { LANGUAGES, t, setLanguage, preferredLanguage, current: () => language, translateTree };
})();
