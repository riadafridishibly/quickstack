import { type ComponentChild, render } from "preact";

/**
 * Registers a custom element that renders a Preact tree into itself.
 *
 * `mount` runs when the element enters the page. It reads props from the
 * element (attributes, server-rendered fallback children, JSON scripts) and
 * returns the tree to render; the fallback children are then replaced.
 *
 * There is no shadow DOM, so Tailwind classes apply and form fields inside
 * the island submit with the surrounding <form>. The element gets a
 * `data-mounted` attribute once rendered; e2e tests wait for it.
 */
export function defineIsland(tag: string, mount: (el: HTMLElement) => ComponentChild): void {
  if (customElements.get(tag)) return;
  customElements.define(
    tag,
    class extends HTMLElement {
      #mounted = false;

      connectedCallback() {
        if (this.#mounted) return;
        this.#mounted = true;
        const tree = mount(this);
        this.replaceChildren();
        render(tree, this);
        this.dataset.mounted = "";
      }

      // Lets htmx (hx-preserve) move the element with moveBefore() without a remount.
      connectedMoveCallback() {}

      disconnectedCallback() {
        // A plain DOM move disconnects and reconnects in the same task; only
        // unmount if the element is really gone.
        queueMicrotask(() => {
          if (this.#mounted && !this.isConnected) {
            render(null, this);
            this.#mounted = false;
            delete this.dataset.mounted;
          }
        });
      }
    },
  );
}

/** Parses <script type="application/json" id={id}>, as rendered by templ.JSONScript. */
export function readJSON<T>(id: string | undefined): T | undefined {
  const text = id ? document.getElementById(id)?.textContent : undefined;
  return text ? (JSON.parse(text) as T) : undefined;
}

/**
 * Registers a custom element that adds behaviour to the server-rendered
 * markup inside it, which stays: the theme decides how it looks, and
 * `setup` finds its parts by `data-part`. `setup` may return a cleanup
 * function, run when the element leaves the page. The element gets
 * `data-mounted` once set up, like an island.
 */
export function defineEnhancement(
  tag: string,
  setup: (el: HTMLElement) => (() => void) | undefined,
): void {
  if (customElements.get(tag)) return;
  customElements.define(
    tag,
    class extends HTMLElement {
      #cleanup: (() => void) | undefined;
      #mounted = false;

      connectedCallback() {
        if (this.#mounted) return;
        this.#mounted = true;
        this.#cleanup = setup(this);
        this.dataset.mounted = "";
      }

      connectedMoveCallback() {}

      disconnectedCallback() {
        queueMicrotask(() => {
          if (this.#mounted && !this.isConnected) {
            this.#cleanup?.();
            this.#cleanup = undefined;
            this.#mounted = false;
            delete this.dataset.mounted;
          }
        });
      }
    },
  );
}
