// The About area: the same words the web site and the editor use.
//
// The words are not written here. `content/about.md` in auto-pigeon-gallery is
// the one human-edited source, its own emitter compiles it into the artefact
// this program serves at `/api/about`, and this file only decides what a
// `paragraph` or a `link` becomes. That is the same division AUP takes for the
// same artefact on `Settings > Config > About` — see
// `auto-pigeon-tools/scripts/about-content.sh apply`, which writes both copies
// from the one authority.
//
// Nothing here builds markup from a string: every node is created with `el` or
// set with `textContent`. The prose is ours, but "the content is trusted" is a
// property that expires silently the day somebody contributes an entry.
//
// # Routes are shown as text, and this is the honest reason
//
// The canonical document links to `/news` and to the legal notices, which are
// pages on the gallery's site. Nothing in this program has a built-in address
// for another component (see AGENTS.md), and the Companion has no gallery
// address to be told, so those links are named rather than pointed at an
// origin this program does not know. A link that went to the wrong host would
// be worse than a link that says which page it means.

"use strict";

(() => {
  const { $, el, api, setMessage } = window.AUCOM;

  // The pane already has an h2 (`area-heading`), and every other area's panels
  // are h3, so the canonical document's own h2 starts there and nests below it.
  const HEADINGS = { 2: "h3", 3: "h4", 4: "h5" };

  function inline(nodes) {
    const out = [];
    for (const node of nodes || []) {
      switch (node.type) {
        case "text":
          out.push(document.createTextNode(node.value));
          break;
        case "strong":
          out.push(el("strong", { children: inline(node.children) }));
          break;
        case "em":
          out.push(el("em", { children: inline(node.children) }));
          break;
        case "code":
          out.push(el("code", { text: node.value }));
          break;
        case "link": {
          const body = inline(node.children);
          if (node.kind === "route") {
            // Named, not linked: see the note at the top of this file.
            out.push(el("span", { className: "muted", attrs: { title: node.href }, children: body }));
            break;
          }
          const attrs = { href: node.href };
          if (node.kind === "external") {
            attrs.target = "_blank";
            attrs.rel = "noreferrer";
          }
          out.push(el("a", { attrs, children: body }));
          break;
        }
        case "image": {
          // The picture's own bytes travel inside the generated artefact as a
          // `data:` URI (`about-content.sh apply`), which the page's
          // `img-src 'self' data:` allows — so it is shown with no request to
          // anywhere, the same picture the gallery shows. Anything that is not an
          // embedded image of a known type is never fetched: the alternative text
          // is shown with the path it wanted.
          const embedded = (assetData || {})[node.src];
          if (typeof embedded === "string" && /^data:image\/(png|jpeg|gif|webp|avif);base64,/.test(embedded)) {
            out.push(el("img", { attrs: { src: embedded, alt: node.alt } }));
          } else {
            out.push(el("span", { className: "muted", attrs: { title: node.src }, text: node.alt }));
          }
          break;
        }
        default:
          // An unknown inline is shown by its own type rather than dropped:
          // a reader seeing `[table]` in their About page knows to update,
          // and a reader seeing nothing at all does not.
          out.push(el("span", { className: "muted", text: "[" + node.type + "]" }));
      }
    }
    return out;
  }

  function blocks(list) {
    const out = [];
    for (const block of list || []) {
      switch (block.type) {
        case "heading":
          out.push(el(HEADINGS[block.level] || "h5", {
            text: undefined,
            attrs: { id: "about-" + block.id },
            children: inline(block.children),
          }));
          break;
        case "paragraph":
          out.push(el("p", { children: inline(block.children) }));
          break;
        case "list": {
          const items = (block.items || []).map((item) => el("li", { children: inline(item) }));
          out.push(el(block.ordered ? "ol" : "ul", { children: items }));
          break;
        }
        case "rule":
          out.push(el("hr"));
          break;
        default:
          out.push(el("p", { className: "muted", text: "[" + block.type + "]" }));
      }
    }
    return out;
  }

  // Every asset the prose names, as `data:` URIs, from the artefact being rendered.
  let assetData = {};

  function render(artefact) {
    const body = $("about-body");
    body.replaceChildren();
    assetData = artefact.asset_data || {};
    // The prose is English only (NEW_243E_AUT_AUP_AUG_AUCOM §B); the shell around it is not.
    body.setAttribute("lang", "en");
    body.dataset.contentDigest = String(artefact.digest || "");

    const about = artefact.about || { meta: {}, blocks: [] };
    body.append(el("h3", { text: about.meta.title || "About", attrs: { id: "about-title" } }));
    body.append(...blocks(about.blocks));

    const news = artefact.news || { meta: {}, intro: [], entries: [] };
    body.append(el("hr"));
    body.append(el("h3", { text: news.meta.title || "News", attrs: { id: "about-news" } }));
    body.append(...blocks(news.intro));
    if ((news.entries || []).length === 0) {
      // An empty list is a fact about the document, and saying it is better
      // than an area that looks like it failed to load.
      body.append(el("p", { className: "muted", text: news.meta.description || "No announcements yet." }));
    } else {
      for (const entry of news.entries) {
        const article = el("article", { className: "panel" });
        article.append(el("h4", { text: entry.meta.title || "" }));
        if (entry.meta.updated) article.append(el("p", { className: "muted", text: entry.meta.updated }));
        article.append(...blocks(entry.blocks));
        body.append(article);
      }
    }

    // One sentence, and only when there is something to explain: the artefact's
    // version, so a reader can tell two builds apart, plus why the web pages
    // are named rather than linked.
    const named = JSON.stringify(artefact).includes('"kind":"route"');
    body.append(el("p", {
      className: "muted",
      text: "Published digest " + String(artefact.digest || "").slice(0, 12) +
        (named ? ". Links to the web site are shown as text: nothing in this program " +
          "has a built-in address for another component, so it does not know where that site is." : ""),
    }));
  }

  async function refresh() {
    const { ok, body } = await api("/api/about");
    if (!ok) {
      setMessage("about-message", body.error || "could not read the About content", "error");
      return;
    }
    setMessage("about-message", "", "");
    render(body);
  }

  window.AUCOM.areas.about = { refresh };
})();
