import htmx from "htmx.org";

// Lets CSS show controls that only work with JavaScript: [.js_&]:flex.
document.documentElement.classList.add("js");

// Don't import app.css here. In dev, Vite would inject it with JS after the
// first paint, flashing unstyled HTML. The layout links it instead
// (internal/web/assets.go).

// The CSP forbids inline styles and eval: .htmx-indicator styles live in
// app.css, and hx-on:* / js: expressions are disabled.
htmx.config.includeIndicatorStyles = false;
htmx.config.allowEval = false;
// Handlers return fragments for HX-Request; a history restore must get the full page.
htmx.config.historyRestoreAsHxRequest = false;
// Swap 422 so handlers can re-render a form with its validation errors,
// and 429 so they can say to wait.
htmx.config.responseHandling = [
  { code: "204", swap: false },
  { code: "[23]..", swap: true },
  { code: "422", swap: true },
  { code: "429", swap: true },
  { code: "[45]..", swap: false, error: true },
];

const requestError = () => document.getElementById("request-error");
document.addEventListener("htmx:beforeRequest", () => {
  requestError()?.setAttribute("hidden", "");
});
for (const event of ["htmx:responseError", "htmx:sendError"]) {
  document.addEventListener(event, () => requestError()?.removeAttribute("hidden"));
}

// Islands: frontend/src/islands/<tag-name>.tsx (or .ts) defines the
// <tag-name> custom element. Each module loads the first time its tag
// appears on the page, including in content swapped in by htmx.
const islands = import.meta.glob(["./islands/*.tsx", "./islands/*.ts", "!./islands/define.ts"]);

htmx.onLoad((node) => {
  if (!(node instanceof Element)) return;
  for (const [path, load] of Object.entries(islands)) {
    const tag = path.slice("./islands/".length).replace(/\.tsx?$/, "");
    if (!customElements.get(tag) && (node.matches(tag) || node.querySelector(tag))) {
      void load();
    }
  }
});

// CSS-only drawers (daisyUI .drawer-toggle): Escape closes an open one and
// returns focus to its toggle, and a page restored from the back/forward
// cache comes back with them closed.
const openDrawers = () => document.querySelectorAll<HTMLInputElement>(".drawer-toggle:checked");
document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape" || e.defaultPrevented || e.isComposing) return;
  for (const t of openDrawers()) {
    const side = t.parentElement?.querySelector(":scope > .drawer-side");
    t.checked = false;
    if (side?.contains(document.activeElement)) t.focus();
  }
});
window.addEventListener("pageshow", (e) => {
  if (!e.persisted) return;
  for (const t of openDrawers()) t.checked = false;
});
