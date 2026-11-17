import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import {
  deleteMarkupBackward,
  insertNewlineContinueMarkup,
  markdownLanguage,
} from "@codemirror/lang-markdown";
import { HighlightStyle, LanguageSupport, syntaxHighlighting } from "@codemirror/language";
import { EditorSelection, EditorState, StateEffect, StateField } from "@codemirror/state";
import { EditorView, keymap, placeholder } from "@codemirror/view";
import { tags } from "@lezer/highlight";
import { render } from "preact";
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import { defineEnhancement } from "./define";

// Server markup (internal/blog/admin.templ, bodyEditor):
//
//   <markdown-editor data-upload-url="/admin/media" data-accept="image/…" data-save-button="save">
//     <textarea name="body" hx-post="/admin/blog/preview" hx-trigger="input delay:400ms" …>
//     <section id="post-preview">
//   </markdown-editor>
//
// The textarea stays in the form, hidden, and the editor writes to it and
// fires `input`, so htmx renders the preview into the section. The editor
// lives in a shadow root: CodeMirror styles a document with a <style>
// element, which the CSP blocks, and a shadow root with constructed
// stylesheets, which it allows.

type View = "write" | "split" | "preview";
const views: { view: View; label: string }[] = [
  { view: "write", label: "Write" },
  { view: "split", label: "Split" },
  { view: "preview", label: "Preview" },
];

// Split needs the width of Tailwind's lg breakpoint.
const wideQuery = "(min-width: 64rem)";

// stored reads and writes per-browser preferences, which may be unavailable.
function stored(storage: () => Storage, key: string, value?: string) {
  try {
    if (value === undefined) return storage().getItem(key);
    storage().setItem(key, value);
  } catch {}
  return null;
}
const viewKey = "markdown-editor:view";
// Full screen lasts the browser session, so it survives the reload after a save.
const fullKey = "markdown-editor:fullscreen";

// Upload ranges: where each image's "Uploading…" text is, kept in step with
// edits so the answer replaces the right text.
type Pending = { id: number; from: number; to: number };
const addPending = StateEffect.define<Pending>();
const donePending = StateEffect.define<number>();
const pendingField = StateField.define<Pending[]>({
  create: () => [],
  update(list, tr) {
    let next = tr.docChanged
      ? list.map((p) => ({
          ...p,
          from: tr.changes.mapPos(p.from, 1),
          to: tr.changes.mapPos(p.to, -1),
        }))
      : list;
    for (const e of tr.effects) {
      if (e.is(addPending)) next = [...next, e.value];
      if (e.is(donePending)) next = next.filter((p) => p.id !== e.value);
    }
    return next;
  },
});

let uploadSeq = 0;

// wrap puts before and after around each selection, or takes them off if
// they are there. An empty selection gets `empty` to type over.
function wrap(view: EditorView, before: string, after: string, empty: string) {
  const { state } = view;
  view.dispatch(
    state.changeByRange((r) => {
      const outer = state.sliceDoc(r.from - before.length, r.to + after.length);
      if (!r.empty && outer === before + state.sliceDoc(r.from, r.to) + after) {
        return {
          changes: [
            { from: r.from - before.length, to: r.from },
            { from: r.to, to: r.to + after.length },
          ],
          range: EditorSelection.range(r.from - before.length, r.to - before.length),
        };
      }
      const text = r.empty ? empty : state.sliceDoc(r.from, r.to);
      return {
        changes: { from: r.from, to: r.to, insert: before + text + after },
        range: EditorSelection.range(r.from + before.length, r.from + before.length + text.length),
      };
    }),
  );
  view.focus();
  return true;
}

function link(view: EditorView) {
  const { state } = view;
  view.dispatch(
    state.changeByRange((r) => {
      const text = r.empty ? "text" : state.sliceDoc(r.from, r.to);
      const insert = `[${text}](https://)`;
      // Select the text to type when there was none, the address otherwise.
      const at = r.empty ? r.from + 1 : r.from + text.length + 3;
      const len = r.empty ? text.length : "https://".length;
      return {
        changes: { from: r.from, to: r.to, insert },
        range: EditorSelection.range(at, at + len),
      };
    }),
  );
  view.focus();
  return true;
}

// toggleLinePrefix adds prefix to each selected line, or removes what
// `existing` matches if every line has it.
function toggleLinePrefix(view: EditorView, prefix: string, existing: RegExp) {
  const { state } = view;
  const lines = new Set<number>();
  for (const r of state.selection.ranges) {
    for (let n = state.doc.lineAt(r.from).number; n <= state.doc.lineAt(r.to).number; n++)
      lines.add(n);
  }
  const all = [...lines].map((n) => state.doc.line(n));
  const remove = all.every((l) => existing.test(l.text));
  const changes = all.map((l) => {
    if (remove) return { from: l.from, to: l.from + (l.text.match(existing)?.[0].length ?? 0) };
    return { from: l.from, insert: prefix };
  });
  view.dispatch({ changes });
  view.focus();
  return true;
}

// insertBlock puts text on lines of its own after the cursor's line, with
// a blank line around it, and selects `select` inside it.
function insertBlock(view: EditorView, text: string, select = "") {
  const { state } = view;
  const line = state.doc.lineAt(state.selection.main.head);
  const at = line.text.trim() === "" ? line.from : line.to;
  const before = at === 0 ? "" : line.text.trim() === "" ? "\n" : "\n\n";
  const after = state.doc.sliceString(at, at + 2) === "\n\n" ? "" : "\n\n";
  const insert = before + text + after;
  const i = select ? text.indexOf(select) : -1;
  const anchor = at + before.length + (i < 0 ? text.length : i);
  view.dispatch({
    changes: { from: at, to: line.text.trim() === "" ? line.to : at, insert },
    selection: EditorSelection.range(anchor, anchor + (i < 0 ? 0 : select.length)),
    scrollIntoView: true,
  });
  view.focus();
  return true;
}

const table = "| Column | Column |\n| ------ | ------ |\n| Cell   | Cell   |";

const alerts = ["Note", "Tip", "Important", "Warning", "Caution"];

// The shortcodes of internal/markdown/shortcode.go.
const shortcodes: { label: string; text: string; select: string }[] = [
  { label: "Figure", text: '{{< figure src="/media/" caption="Caption" >}}', select: "/media/" },
  { label: "YouTube video", text: '{{< youtube id="" >}}', select: "" },
  { label: "Vimeo video", text: '{{< vimeo id="" >}}', select: "" },
  {
    label: "Details",
    text: '{{< details summary="More" >}}\nHidden until opened.\n{{< /details >}}',
    select: "More",
  },
  {
    label: "Tabs",
    text: '{{< tabs >}}\n{{< tab label="First" >}}\nText\n{{< /tab >}}\n{{< tab label="Second" >}}\nText\n{{< /tab >}}\n{{< /tabs >}}',
    select: "First",
  },
];

// replacePending puts text where upload id's placeholder is, if the
// placeholder is still there.
function replacePending(view: EditorView, id: number, expected: string, text: string) {
  const p = view.state.field(pendingField).find((x) => x.id === id);
  const effects = donePending.of(id);
  if (!p || view.state.sliceDoc(p.from, p.to) !== expected) {
    view.dispatch({ effects });
    return;
  }
  // Drop the blank line left behind by a failed upload.
  let to = p.to;
  if (text === "" && view.state.sliceDoc(to, to + 2) === "\n\n") to += 2;
  view.dispatch({ changes: { from: p.from, to, insert: text }, effects });
}

function imageFiles(data: DataTransfer | null): File[] {
  if (!data || data.types.includes("text/rtf")) return [];
  return Array.from(data.files);
}

type Props = {
  textarea: HTMLTextAreaElement;
  host: HTMLElement;
  uploadURL: string;
  accept: string;
  saveButton: string;
};

function MarkdownEditor({ textarea, host, uploadURL, accept, saveButton }: Props) {
  const shadowHost = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const insertMenu = useRef<HTMLDetailsElement>(null);
  const [view, setView] = useState<View>(() => {
    const v = stored(() => localStorage, viewKey);
    return v === "write" || v === "preview" ? v : "split";
  });
  const [wide, setWide] = useState(() => matchMedia(wideQuery).matches);
  const [full, setFull] = useState(() => stored(() => sessionStorage, fullKey) === "1");
  const shown: View = view === "split" && !wide ? "write" : view;
  const [error, setError] = useState("");
  const [message, setMessage] = useState(host.dataset.message ?? "");

  const upload = (view: EditorView, files: File[], at: number) => {
    if (files.length === 0) return;
    setError("");
    const items = files.map((file) => {
      const name = file.name.replace(/[[\]\\\n]/g, "") || "image";
      return { file, id: ++uploadSeq, text: `![Uploading ${name}…]()` };
    });
    const line = view.state.doc.lineAt(at);
    const own = line.text.trim() === "";
    const before = own ? "" : "\n\n";
    let pos = (own ? line.from : at) + before.length;
    const effects = items.map((it, i) => {
      const p = { id: it.id, from: pos, to: pos + it.text.length };
      pos = p.to + (i < items.length - 1 ? 2 : 0);
      return addPending.of(p);
    });
    const insert = `${before}${items.map((it) => it.text).join("\n\n")}\n\n`;
    view.dispatch({
      changes: { from: own ? line.from : at, to: own ? line.to : at, insert },
      selection: { anchor: pos + 2 },
      effects,
      scrollIntoView: true,
    });
    for (const it of items) {
      const body = new FormData();
      body.append("file", it.file, it.file.name);
      fetch(uploadURL, { method: "POST", headers: { Accept: "application/json" }, body })
        .then(async (res) => {
          const data = (await res.json().catch(() => ({}))) as {
            markdown?: string;
            error?: string;
          };
          if (res.ok && data.markdown) {
            replacePending(view, it.id, it.text, data.markdown);
            return;
          }
          replacePending(view, it.id, it.text, "");
          setError(
            `${it.file.name}: ${data.error ?? "The image couldn’t be uploaded. Try again."}`,
          );
        })
        .catch(() => {
          replacePending(view, it.id, it.text, "");
          setError(
            `${it.file.name}: The image couldn’t be uploaded. Check your connection and try again.`,
          );
        });
    }
  };

  const save = () => {
    const form = textarea.form;
    const button = form?.querySelector<HTMLButtonElement>(
      `button[name="action"][value="${saveButton}"]`,
    );
    form?.requestSubmit(button ?? undefined);
    return true;
  };

  useEffect(() => {
    const parent = shadowHost.current;
    if (!parent) return;
    const root = parent.attachShadow({ mode: "open" });
    const view = new EditorView({
      parent: root,
      root,
      state: EditorState.create({
        doc: textarea.value,
        extensions: [
          history(),
          pendingField,
          keymap.of([
            { key: "Mod-b", run: (v) => wrap(v, "**", "**", "bold text") },
            { key: "Mod-i", run: (v) => wrap(v, "_", "_", "italic text") },
            { key: "Mod-k", run: link },
            { key: "Mod-s", run: save, preventDefault: true },
            { key: "Enter", run: insertNewlineContinueMarkup },
            { key: "Backspace", run: deleteMarkupBackward },
            ...defaultKeymap,
            ...historyKeymap,
          ]),
          // Not markdown() from lang-markdown, which would bring the HTML,
          // CSS and JavaScript languages for HTML in posts, which the
          // renderer leaves out.
          new LanguageSupport(markdownLanguage),
          syntaxHighlighting(highlight),
          EditorView.lineWrapping,
          placeholder("Write the post in Markdown…"),
          EditorView.contentAttributes.of({ "aria-label": "Body" }),
          theme,
          EditorView.updateListener.of((u) => {
            if (!u.docChanged) return;
            setMessage("");
            textarea.value = u.state.doc.toString();
            textarea.dispatchEvent(new Event("input", { bubbles: true }));
          }),
          EditorView.domEventHandlers({
            paste(e, v) {
              const files = imageFiles(e.clipboardData);
              if (files.length === 0) return false;
              e.preventDefault();
              upload(v, files, v.state.selection.main.head);
              return true;
            },
            drop(e, v) {
              const files = imageFiles(e.dataTransfer);
              if (files.length === 0) return false;
              e.preventDefault();
              upload(
                v,
                files,
                v.posAtCoords({ x: e.clientX, y: e.clientY }) ?? v.state.selection.main.head,
              );
              return true;
            },
          }),
        ],
      }),
    });
    viewRef.current = view;
    // The preview of the body as loaded.
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
    return () => {
      view.destroy();
      viewRef.current = null;
    };
  }, []);

  useEffect(() => {
    const q = matchMedia(wideQuery);
    const onChange = () => setWide(q.matches);
    q.addEventListener("change", onChange);
    return () => q.removeEventListener("change", onChange);
  }, []);

  // Layout effects, so the host's layout changes in the same frame as the
  // controls.
  useLayoutEffect(() => {
    host.dataset.view = shown;
  }, [shown]);

  useLayoutEffect(() => {
    host.toggleAttribute("data-fullscreen", full);
    document.documentElement.classList.toggle("overflow-hidden", full);
    stored(() => sessionStorage, fullKey, full ? "1" : "");
    if (!full) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !e.defaultPrevented) setFull(false);
    };
    // A save that fails re-renders the form with its errors, which full
    // screen would cover.
    const onSwap = () => {
      if (textarea.form?.querySelector('[aria-invalid="true"]')) setFull(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("htmx:after:swap", onSwap);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("htmx:after:swap", onSwap);
      document.documentElement.classList.remove("overflow-hidden");
    };
  }, [full]);

  // The Insert menu closes on Escape and on a click outside it.
  useEffect(() => {
    const menu = insertMenu.current;
    if (!menu) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || !menu.open) return;
      e.preventDefault();
      menu.open = false;
      menu.querySelector("summary")?.focus();
    };
    const onClick = (e: MouseEvent) => {
      if (menu.open && !menu.contains(e.target as Node)) menu.open = false;
    };
    menu.addEventListener("keydown", onKey);
    document.addEventListener("click", onClick);
    return () => {
      menu.removeEventListener("keydown", onKey);
      document.removeEventListener("click", onClick);
    };
  }, []);

  const run = (f: (v: EditorView) => unknown) => () => {
    const v = viewRef.current;
    if (v) f(v);
  };
  const insert = (text: string, select: string) => () => {
    if (insertMenu.current) insertMenu.current.open = false;
    const v = viewRef.current;
    if (v) insertBlock(v, text, select);
  };

  const tool = "btn btn-ghost btn-sm";
  return (
    <>
      <div class="col-span-full flex flex-wrap items-center gap-2">
        <fieldset aria-label="Layout" class="join">
          {views
            .filter((v) => wide || v.view !== "split")
            .map((v) => (
              <button
                key={v.view}
                type="button"
                aria-pressed={shown === v.view}
                class={`btn btn-sm join-item ${shown === v.view ? "btn-active" : ""}`}
                onClick={() => {
                  setView(v.view);
                  stored(() => localStorage, viewKey, v.view);
                }}
              >
                {v.label}
              </button>
            ))}
        </fieldset>
        {full && message && (
          <p role="status" class="ml-auto text-sm text-success">
            {message}
          </p>
        )}
        <button
          type="button"
          aria-pressed={full}
          title={full ? "Exit full screen (Esc)" : "Full screen"}
          class={`btn btn-ghost btn-sm ${full && message ? "" : "ml-auto"}`}
          onClick={() => setFull(!full)}
        >
          {full ? "Exit full screen" : "Full screen"}
        </button>
      </div>
      <div class={`flex min-h-0 min-w-0 flex-col gap-2 ${shown === "preview" ? "hidden" : ""}`}>
        <div role="toolbar" aria-label="Formatting" class="flex flex-wrap items-center gap-0.5">
          <button
            type="button"
            class={tool}
            title="Heading"
            onClick={run((v) => toggleLinePrefix(v, "## ", /^#{1,6} /))}
          >
            H
          </button>
          <button
            type="button"
            class={`${tool} font-bold`}
            title="Bold (Ctrl+B)"
            aria-keyshortcuts="Control+B Meta+B"
            onClick={run((v) => wrap(v, "**", "**", "bold text"))}
          >
            B
          </button>
          <button
            type="button"
            class={`${tool} italic`}
            title="Italic (Ctrl+I)"
            aria-keyshortcuts="Control+I Meta+I"
            onClick={run((v) => wrap(v, "_", "_", "italic text"))}
          >
            I
          </button>
          <button
            type="button"
            class={tool}
            title="Link (Ctrl+K)"
            aria-keyshortcuts="Control+K Meta+K"
            onClick={run(link)}
          >
            Link
          </button>
          <button
            type="button"
            class={tool}
            onClick={run((v) => toggleLinePrefix(v, "- ", /^\s*[-*+] /))}
          >
            List
          </button>
          <button type="button" class={tool} onClick={run((v) => insertBlock(v, table, "Column"))}>
            Table
          </button>
          <button type="button" class={tool} onClick={() => fileInput.current?.click()}>
            Image
          </button>
          <details ref={insertMenu} class="dropdown">
            <summary class={tool}>Insert</summary>
            <ul class="menu dropdown-content z-10 w-52 rounded-box border border-base-300 bg-base-100 p-2 shadow-sm">
              <li class="menu-title">Alerts</li>
              {alerts.map((a) => (
                <li key={a}>
                  <button type="button" onClick={insert(`> [!${a.toUpperCase()}]\n> Text`, "Text")}>
                    {a}
                  </button>
                </li>
              ))}
              <li class="menu-title">Shortcodes</li>
              {shortcodes.map((s) => (
                <li key={s.label}>
                  <button type="button" onClick={insert(s.text, s.select)}>
                    {s.label}
                  </button>
                </li>
              ))}
            </ul>
          </details>
          <input
            ref={fileInput}
            type="file"
            accept={accept}
            multiple
            hidden
            onChange={(e) => {
              const input = e.currentTarget;
              const v = viewRef.current;
              if (v && input.files) upload(v, Array.from(input.files), v.state.selection.main.head);
              input.value = "";
            }}
          />
        </div>
        <div
          ref={shadowHost}
          class="textarea block h-auto min-h-0 w-full p-0 in-data-fullscreen:flex-1 in-data-fullscreen:[--editor-height:100%]"
        />
        {error && (
          <p role="alert" class="alert alert-error alert-soft flex justify-between text-sm">
            {error}
            <button type="button" class="btn btn-ghost btn-xs" onClick={() => setError("")}>
              Dismiss
            </button>
          </p>
        )}
      </div>
    </>
  );
}

// Colors are the theme's, which reach into the shadow root as custom
// properties.
const muted = "color-mix(in oklab, var(--color-base-content) 55%, transparent)";

const theme = EditorView.theme({
  // The host sets these in full screen.
  "&": {
    color: "var(--color-base-content)",
    fontSize: "0.875rem",
    height: "var(--editor-height, auto)",
    maxHeight: "var(--editor-max-height, 70vh)",
  },
  "&.cm-focused": { outline: "none" },
  ".cm-scroller": {
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
    lineHeight: "1.6",
  },
  ".cm-content": {
    minHeight: "24rem",
    padding: "0.75rem 0",
    caretColor: "var(--color-base-content)",
  },
  ".cm-line": { padding: "0 0.75rem" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--color-base-content)" },
  "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection":
    { backgroundColor: "color-mix(in oklab, var(--color-primary) 25%, transparent)" },
  ".cm-placeholder": { color: muted },
});

const highlight = HighlightStyle.define([
  { tag: tags.heading, fontWeight: "700", color: "var(--color-primary)" },
  { tag: tags.strong, fontWeight: "700" },
  { tag: tags.emphasis, fontStyle: "italic" },
  { tag: tags.strikethrough, textDecoration: "line-through" },
  { tag: [tags.link, tags.url], color: "var(--color-info)" },
  { tag: tags.monospace, color: "var(--color-secondary)" },
  { tag: [tags.processingInstruction, tags.contentSeparator, tags.labelName], color: muted },
  { tag: tags.quote, fontStyle: "italic" },
]);

defineEnhancement("markdown-editor", (el) => {
  const textarea = el.querySelector<HTMLTextAreaElement>("textarea");
  if (!textarea) throw new Error("<markdown-editor> needs a <textarea>");
  const container = document.createElement("div");
  container.className = "contents";
  el.prepend(container);
  textarea.hidden = true;
  render(
    <MarkdownEditor
      textarea={textarea}
      host={el}
      uploadURL={el.dataset.uploadUrl ?? ""}
      accept={el.dataset.accept ?? ""}
      saveButton={el.dataset.saveButton ?? ""}
    />,
    container,
  );
  return () => {
    render(null, container);
    container.remove();
    textarea.hidden = false;
    delete el.dataset.view;
    el.removeAttribute("data-fullscreen");
  };
});
