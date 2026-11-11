package projects

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/tenant"
	"github.com/riadafridishibly/quickstack/internal/web"
)

// createTask adds a task to the project's To do column. htmx gets the board
// back, with the form emptied or showing its errors.
func (h handler) createTask(w http.ResponseWriter, r *http.Request) error {
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	v := projectView{Form: taskFormFrom(r)}
	ok := false
	err = h.tdb.Tx(r.Context(), func(tx tenant.Tx) error {
		if err := h.load(r.Context(), tx, id, &v); err != nil {
			return err
		}
		if ok = v.Form.validate(v.Members); !ok {
			return nil
		}
		_, err := tx.CreateTask(r.Context(), db.CreateTaskParams{
			WorkspaceID: tx.WorkspaceID, ProjectID: id, Title: v.Form.Title, AssigneeID: v.Form.assigneeID, DueOn: v.Form.dueOn,
		})
		if err != nil {
			return err
		}
		if err := tx.TouchProject(r.Context(), db.TouchProjectParams{WorkspaceID: tx.WorkspaceID, ID: id}); err != nil {
			return err
		}
		v.Tasks, err = tx.ListTasks(r.Context(), db.ListTasksParams{WorkspaceID: tx.WorkspaceID, ProjectID: id})
		return err
	})
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	status := http.StatusOK
	if !ok {
		status = http.StatusUnprocessableEntity
	} else {
		v.Form = taskForm{Errors: map[string]string{}}
	}
	if web.IsHTMX(r) {
		return web.Render(w, r, status, board(v))
	}
	if !ok {
		return web.Render(w, r, status, projectPage(v))
	}
	web.Redirect(w, r, projectURL(r.Context(), id))
	return nil
}

type taskView struct {
	ProjectID   int64
	ProjectName string
	TaskID      int64
	Form        taskForm
	Members     []db.ListMembersRow
}

func taskIDs(r *http.Request) (project, task int64, err error) {
	if project, err = web.PathID(r, "id"); err != nil {
		return 0, 0, err
	}
	task, err = web.PathID(r, "task")
	return project, task, err
}

func (h handler) loadTask(r *http.Request, tx tenant.Tx, v *taskView) (db.Task, error) {
	p, err := tx.GetProject(r.Context(), db.GetProjectParams{WorkspaceID: tx.WorkspaceID, ID: v.ProjectID})
	if err != nil {
		return db.Task{}, err
	}
	v.ProjectName = p.Name
	t, err := tx.GetTask(r.Context(), db.GetTaskParams{WorkspaceID: tx.WorkspaceID, ProjectID: v.ProjectID, ID: v.TaskID})
	if err != nil {
		return t, err
	}
	v.Members, err = tx.ListMembers(r.Context(), tx.WorkspaceID)
	return t, err
}

func (h handler) editTask(w http.ResponseWriter, r *http.Request) error {
	pid, tid, err := taskIDs(r)
	if err != nil {
		return err
	}
	v := taskView{ProjectID: pid, TaskID: tid}
	err = h.tdb.Tx(r.Context(), func(tx tenant.Tx) error {
		t, err := h.loadTask(r, tx, &v)
		v.Form = taskFormOf(t)
		return err
	})
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	return web.Render(w, r, http.StatusOK, taskPage(v))
}

func (h handler) updateTask(w http.ResponseWriter, r *http.Request) error {
	pid, tid, err := taskIDs(r)
	if err != nil {
		return err
	}
	v := taskView{ProjectID: pid, TaskID: tid, Form: taskFormFrom(r)}
	ok := false
	err = h.tdb.Tx(r.Context(), func(tx tenant.Tx) error {
		if _, err := h.loadTask(r, tx, &v); err != nil {
			return err
		}
		if ok = v.Form.validate(v.Members); !ok {
			return nil
		}
		_, err := tx.UpdateTask(r.Context(), db.UpdateTaskParams{
			WorkspaceID: tx.WorkspaceID, ProjectID: pid, ID: tid, Title: v.Form.Title, AssigneeID: v.Form.assigneeID, DueOn: v.Form.dueOn,
		})
		if err != nil {
			return err
		}
		return tx.TouchProject(r.Context(), db.TouchProjectParams{WorkspaceID: tx.WorkspaceID, ID: pid})
	})
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	if !ok {
		return web.Render(w, r, http.StatusUnprocessableEntity, taskPage(v))
	}
	web.Redirect(w, r, projectURL(r.Context(), pid))
	return nil
}

// moveTask puts a task in a column. The board island also sends the
// column's new order and asks for no content, since it already shows the
// result; htmx gets the board back.
func (h handler) moveTask(w http.ResponseWriter, r *http.Request) error {
	pid, tid, err := taskIDs(r)
	if err != nil {
		return err
	}
	status := r.PostFormValue("status")
	order, ok := parseOrder(r.PostFormValue("order"))
	if !slices.Contains(Statuses, status) || !ok || (len(order) > 0 && !slices.Contains(order, tid)) {
		http.Error(w, "bad move", http.StatusBadRequest)
		return nil
	}
	v := projectView{Form: taskForm{Errors: map[string]string{}}}
	err = h.tdb.Tx(r.Context(), func(tx tenant.Tx) error {
		n, err := tx.MoveTask(r.Context(), db.MoveTaskParams{WorkspaceID: tx.WorkspaceID, ProjectID: pid, ID: tid, Status: status})
		if err != nil {
			return err
		}
		if n == 0 {
			return web.ErrNotFound
		}
		if len(order) > 0 {
			if _, err := tx.ReorderTasks(r.Context(), db.ReorderTasksParams{WorkspaceID: tx.WorkspaceID, ProjectID: pid, Status: status, Ids: order}); err != nil {
				return err
			}
		}
		if err := tx.TouchProject(r.Context(), db.TouchProjectParams{WorkspaceID: tx.WorkspaceID, ID: pid}); err != nil {
			return err
		}
		if web.WantsNoContent(r) {
			return nil
		}
		return h.load(r.Context(), tx, pid, &v)
	})
	if err != nil {
		return err
	}
	switch {
	case web.WantsNoContent(r):
		w.WriteHeader(http.StatusNoContent)
		return nil
	case web.IsHTMX(r):
		return web.Render(w, r, http.StatusOK, board(v))
	}
	web.Redirect(w, r, projectURL(r.Context(), pid))
	return nil
}

func (h handler) deleteTask(w http.ResponseWriter, r *http.Request) error {
	pid, tid, err := taskIDs(r)
	if err != nil {
		return err
	}
	var n int64
	err = h.tdb.Tx(r.Context(), func(tx tenant.Tx) (err error) {
		n, err = tx.DeleteTask(r.Context(), db.DeleteTaskParams{WorkspaceID: tx.WorkspaceID, ProjectID: pid, ID: tid})
		return err
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return web.ErrNotFound
	}
	web.Redirect(w, r, projectURL(r.Context(), pid))
	return nil
}

// boardData is what the task-board island reads from its JSON script.
type boardData struct {
	MoveURL string        `json:"moveURL"`
	Columns []boardColumn `json:"columns"`
}

type boardColumn struct {
	Status string      `json:"status"`
	Label  string      `json:"label"`
	Tasks  []boardTask `json:"tasks"`
}

type boardTask struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Assignee string `json:"assignee,omitempty"`
	Due      string `json:"due,omitempty"`
	URL      string `json:"url"`
}

type column struct {
	Status string
	Tasks  []db.ListTasksRow
}

func columns(tasks []db.ListTasksRow) []column {
	cols := make([]column, len(Statuses))
	for i, s := range Statuses {
		cols[i].Status = s
	}
	for _, t := range tasks {
		if i := slices.Index(Statuses, t.Status); i >= 0 {
			cols[i].Tasks = append(cols[i].Tasks, t)
		}
	}
	return cols
}

func newBoardData(base string, tasks []db.ListTasksRow) boardData {
	d := boardData{MoveURL: base + "/tasks/"}
	for _, c := range columns(tasks) {
		bc := boardColumn{Status: c.Status, Label: StatusLabel(c.Status), Tasks: []boardTask{}}
		for _, t := range c.Tasks {
			bt := boardTask{ID: t.ID, Title: t.Title, URL: base + "/tasks/" + strconv.FormatInt(t.ID, 10)}
			if t.AssigneeName != nil {
				bt.Assignee = *t.AssigneeName
			}
			if t.DueOn != nil {
				bt.Due = t.DueOn.Format("Jan 2")
			}
			bc.Tasks = append(bc.Tasks, bt)
		}
		d.Columns = append(d.Columns, bc)
	}
	return d
}
