import htmx from "htmx.org";

// Lets CSS show controls that only work with JavaScript: [.js_&]:flex.
document.documentElement.classList.add("js");

// Don't import app.css here. In dev, Vite would inject it with JS after the
// first paint, flashing unstyled HTML. The layout links it instead
// (internal/web/assets.go).

// htmx swaps every status but these. 422 and 429 swap, so handlers can
// re-render a form with its validation errors or say to wait. The type
// says number[], but htmx also matches patterns like "5xx".
const swapped = new Set([422, 429]);
htmx.config.noSwap = [204, 304, "40x", "41x", "5xx"] as unknown as number[];

const requestError = () => document.getElementById("request-error");
const showRequestError = () => requestError()?.removeAttribute("hidden");
document.addEventListener("htmx:before:request", () => {
  requestError()?.setAttribute("hidden", "");
});
document.addEventListener("htmx:response:error", (e) => {
  if (!swapped.has(e.detail.ctx.response?.status ?? 0)) showRequestError();
});
document.addEventListener("htmx:error", showRequestError);

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
