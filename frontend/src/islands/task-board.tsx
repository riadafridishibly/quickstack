import { useState } from "preact/hooks";
import { defineIsland, readJSON } from "./define";

// The board's data, from newBoardData in internal/projects/tasks.go.
type Task = { id: number; title: string; assignee?: string; due?: string; url: string };
type Column = { status: string; label: string; tasks: Task[] };
type Data = { moveURL: string; columns: Column[] };

type Drag = { id: number; from: string };

// move returns the columns with task id taken out of its column and put in
// the column `to` before index `at` (the end when at is -1).
function move(columns: Column[], id: number, to: string, at: number): Column[] {
  const task = columns.flatMap((c) => c.tasks).find((t) => t.id === id);
  if (!task) return columns;
  return columns.map((c) => {
    const tasks = c.tasks.filter((t) => t.id !== id);
    if (c.status === to) tasks.splice(at < 0 ? tasks.length : Math.min(at, tasks.length), 0, task);
    return { ...c, tasks };
  });
}

function TaskBoard({ data }: { data: Data }) {
  const [columns, setColumns] = useState(data.columns);
  const [drag, setDrag] = useState<Drag | null>(null);
  const [over, setOver] = useState<string | null>(null);
  const [error, setError] = useState("");

  // save sends the move, and puts the board back if the server refuses it.
  const save = async (next: Column[], id: number, status: string) => {
    const before = columns;
    setColumns(next);
    setError("");
    const order = next.find((c) => c.status === status)?.tasks.map((t) => t.id) ?? [];
    try {
      const res = await fetch(`${data.moveURL}${id}/move`, {
        method: "POST",
        headers: { Prefer: "return=minimal" },
        body: new URLSearchParams({ status, order: order.join(",") }),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
    } catch {
      setColumns(before);
      setError("The task couldn’t be moved. Try again.");
    }
  };

  // dropIndex is where in the column a drop at clientY lands: before the
  // first card whose middle is below the pointer.
  const dropIndex = (list: HTMLElement, clientY: number) => {
    const cards = Array.from(list.querySelectorAll<HTMLElement>("[data-task-id]")).filter(
      (el) => Number(el.dataset.taskId) !== drag?.id,
    );
    const i = cards.findIndex((el) => {
      const r = el.getBoundingClientRect();
      return clientY < r.top + r.height / 2;
    });
    return i;
  };

  return (
    <div>
      {error && (
        <p role="alert" class="alert alert-error alert-soft mb-3">
          {error}
        </p>
      )}
      <div class="grid gap-4 md:grid-cols-3">
        {columns.map((col, ci) => (
          // biome-ignore lint/a11y/noStaticElementInteractions: a drop target for the pointer; keyboard users have the arrow buttons.
          <section
            key={col.status}
            data-column={col.status}
            class={`rounded-box p-3 transition-colors ${over === col.status ? "bg-primary/10 ring-2 ring-primary/40" : "bg-base-300/40"}`}
            onDragOver={(e) => {
              if (!drag) return;
              e.preventDefault();
              setOver(col.status);
            }}
            onDragLeave={(e) => {
              if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setOver(null);
            }}
            onDrop={(e) => {
              e.preventDefault();
              setOver(null);
              if (!drag) return;
              const list = e.currentTarget.querySelector("ul");
              const at = list ? dropIndex(list, e.clientY) : -1;
              void save(move(columns, drag.id, col.status, at), drag.id, col.status);
              setDrag(null);
            }}
          >
            <h2 class="mb-3 flex items-center justify-between px-1 text-sm font-semibold">
              {col.label}
              <span class="badge badge-sm badge-ghost tabular-nums">{col.tasks.length}</span>
            </h2>
            <ul class="min-h-12 space-y-2">
              {col.tasks.map((t) => (
                <li
                  key={t.id}
                  data-task={t.title}
                  data-task-id={t.id}
                  draggable
                  onDragStart={(e) => {
                    e.dataTransfer?.setData("text/plain", String(t.id));
                    setDrag({ id: t.id, from: col.status });
                  }}
                  onDragEnd={() => {
                    setDrag(null);
                    setOver(null);
                  }}
                  class={`group cursor-grab rounded-box border border-base-300 bg-base-100 p-3 shadow-xs active:cursor-grabbing ${drag?.id === t.id ? "opacity-40" : ""}`}
                >
                  <div class="flex items-start justify-between gap-2">
                    <a href={t.url} class="font-medium link-hover" draggable={false}>
                      {t.title}
                    </a>
                    <span class="flex shrink-0 gap-0.5 opacity-60 group-hover:opacity-100 focus-within:opacity-100">
                      {ci > 0 && (
                        <button
                          type="button"
                          class="btn btn-ghost btn-xs btn-square"
                          aria-label={`Move “${t.title}” to ${columns[ci - 1]?.label}`}
                          onClick={() => {
                            const to = columns[ci - 1]?.status ?? col.status;
                            void save(move(columns, t.id, to, -1), t.id, to);
                          }}
                        >
                          ←
                        </button>
                      )}
                      {ci < columns.length - 1 && (
                        <button
                          type="button"
                          class="btn btn-ghost btn-xs btn-square"
                          aria-label={`Move “${t.title}” to ${columns[ci + 1]?.label}`}
                          onClick={() => {
                            const to = columns[ci + 1]?.status ?? col.status;
                            void save(move(columns, t.id, to, -1), t.id, to);
                          }}
                        >
                          →
                        </button>
                      )}
                    </span>
                  </div>
                  {(t.assignee || t.due) && (
                    <p class="mt-1 flex flex-wrap gap-x-3 text-xs opacity-70">
                      {t.assignee && <span>{t.assignee}</span>}
                      {t.due && <span>Due {t.due}</span>}
                    </p>
                  )}
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </div>
  );
}

// Server markup (internal/projects/views.templ):
//
//   <script type="application/json" id="board-data">…</script>
//   <task-board data-json="board-data">
//     …the columns, each card with a Move form…
//   </task-board>
//
// Without JavaScript the columns' Move forms post to the same endpoint. The
// island replaces them with cards that can be dragged between and within
// columns, or moved with the arrow buttons.
defineIsland("task-board", (el) => {
  const data = readJSON<Data>(el.dataset.json);
  if (!data) throw new Error("<task-board> needs data-json");
  return <TaskBoard data={data} />;
});
