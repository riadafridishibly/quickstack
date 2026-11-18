// Package projects is the reference feature: a list with live search and a
// filter, create and edit forms that work without JavaScript, a task board
// that htmx updates in place, and an island that adds drag and drop.
package projects

import (
	"context"
	"net/http"
	"strconv"

	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/tenant"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	listTarget  = "project-list"
	boardTarget = "board"
)

type handler struct{ tdb *tenant.DB }

func Routes(r *auth.WorkspaceRouter, tdb *tenant.DB) {
	h := handler{tdb: tdb}
	r.Handle("GET /w/{ws}/projects", auth.AnyMember, h.list)
	r.Handle("GET /w/{ws}/projects/new", auth.ManageProjects, h.newProject)
	r.Handle("POST /w/{ws}/projects", auth.ManageProjects, h.create)
	r.Handle("GET /w/{ws}/projects/{id}", auth.AnyMember, h.show)
	r.Handle("GET /w/{ws}/projects/{id}/edit", auth.ManageProjects, h.edit)
	r.Handle("POST /w/{ws}/projects/{id}", auth.ManageProjects, h.update)
	r.Handle("POST /w/{ws}/projects/{id}/delete", auth.ManageProjects, h.delete)

	r.Handle("POST /w/{ws}/projects/{id}/tasks", auth.EditTasks, h.createTask)
	r.Handle("GET /w/{ws}/projects/{id}/tasks/{task}", auth.EditTasks, h.editTask)
	r.Handle("POST /w/{ws}/projects/{id}/tasks/{task}", auth.EditTasks, h.updateTask)
	r.Handle("POST /w/{ws}/projects/{id}/tasks/{task}/move", auth.EditTasks, h.moveTask)
	r.Handle("POST /w/{ws}/projects/{id}/tasks/{task}/delete", auth.EditTasks, h.deleteTask)
}

func projectURL(ctx context.Context, id int64) string {
	return tenant.From(ctx).URL("/projects/" + strconv.FormatInt(id, 10))
}

type listView struct {
	Projects []db.ListProjectsRow
	Query    string
	Status   string
	Page     web.Page
}

func (h handler) list(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	v := listView{Query: q.Get("q"), Status: q.Get("status"), Page: web.PageFrom(r, web.PageSize)}
	if v.Status != StatusArchived && v.Status != "all" {
		v.Status = StatusActive
	}
	status := v.Status
	if status == "all" {
		status = ""
	}
	err := h.tdb.Tx(r.Context(), func(tx tenant.Tx) (err error) {
		v.Projects, err = web.LoadPage(&v.Page, func(p db.ListProjectsRow) int64 { return p.Total },
			func(limit, offset int32) ([]db.ListProjectsRow, error) {
				return tx.ListProjects(r.Context(), db.ListProjectsParams{
					WorkspaceID: tx.WorkspaceID, Status: status, Query: db.EscapeLike(v.Query), Lim: limit, Off: offset,
				})
			})
		return err
	})
	if err != nil {
		return err
	}
	if web.HXTarget(r) == listTarget {
		return web.Render(w, r, http.StatusOK, projectList(v))
	}
	return web.Render(w, r, http.StatusOK, listPage(v))
}

func (h handler) newProject(w http.ResponseWriter, r *http.Request) error {
	return web.Render(w, r, http.StatusOK, projectFormPage(0, projectForm{Status: StatusActive, Errors: map[string]string{}}))
}

func (h handler) create(w http.ResponseWriter, r *http.Request) error {
	f := projectFormFrom(r)
	if !f.validate() {
		return renderProjectForm(w, r, 0, f)
	}
	var id int64
	err := h.tdb.Tx(r.Context(), func(tx tenant.Tx) (err error) {
		id, err = tx.CreateProject(r.Context(), db.CreateProjectParams{
			WorkspaceID: tx.WorkspaceID, Name: f.Name, Description: f.Description, Status: f.Status, Public: f.Public,
		})
		return err
	})
	if err != nil {
		return err
	}
	web.Redirect(w, r, projectURL(r.Context(), id))
	return nil
}

// renderProjectForm re-renders a form that failed validation: the form
// alone for htmx, the whole page otherwise.
func renderProjectForm(w http.ResponseWriter, r *http.Request, id int64, f projectForm) error {
	if web.IsHTMX(r) {
		return web.Render(w, r, http.StatusUnprocessableEntity, projectFormEl(id, f))
	}
	return web.Render(w, r, http.StatusUnprocessableEntity, projectFormPage(id, f))
}

type projectView struct {
	Project db.Project
	Tasks   []db.ListTasksRow
	Members []db.ListMembersRow
	Form    taskForm
}

func (h handler) load(ctx context.Context, tx tenant.Tx, id int64, v *projectView) (err error) {
	if v.Project, err = tx.GetProject(ctx, db.GetProjectParams{WorkspaceID: tx.WorkspaceID, ID: id}); err != nil {
		return err
	}
	if v.Tasks, err = tx.ListTasks(ctx, db.ListTasksParams{WorkspaceID: tx.WorkspaceID, ProjectID: id}); err != nil {
		return err
	}
	v.Members, err = tx.ListMembers(ctx, tx.WorkspaceID)
	return err
}

func (h handler) show(w http.ResponseWriter, r *http.Request) error {
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	v := projectView{Form: taskForm{Errors: map[string]string{}}}
	if err := h.tdb.Tx(r.Context(), func(tx tenant.Tx) error { return h.load(r.Context(), tx, id, &v) }); err != nil {
		return web.NotFoundIfNoRows(err)
	}
	if web.HXTarget(r) == boardTarget {
		return web.Render(w, r, http.StatusOK, board(v))
	}
	return web.Render(w, r, http.StatusOK, projectPage(v))
}

func (h handler) edit(w http.ResponseWriter, r *http.Request) error {
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	var p db.Project
	err = h.tdb.Tx(r.Context(), func(tx tenant.Tx) (err error) {
		p, err = tx.GetProject(r.Context(), db.GetProjectParams{WorkspaceID: tx.WorkspaceID, ID: id})
		return err
	})
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	return web.Render(w, r, http.StatusOK, projectFormPage(id, projectFormOf(p)))
}

func (h handler) update(w http.ResponseWriter, r *http.Request) error {
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	f := projectFormFrom(r)
	ok := f.validate()
	err = h.tdb.Tx(r.Context(), func(tx tenant.Tx) error {
		// Checked first, so a missing project is a 404 even when the form
		// is invalid.
		if _, err := tx.GetProject(r.Context(), db.GetProjectParams{WorkspaceID: tx.WorkspaceID, ID: id}); err != nil || !ok {
			return err
		}
		_, err := tx.UpdateProject(r.Context(), db.UpdateProjectParams{
			WorkspaceID: tx.WorkspaceID, ID: id, Name: f.Name, Description: f.Description, Status: f.Status, Public: f.Public,
		})
		return err
	})
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	if !ok {
		return renderProjectForm(w, r, id, f)
	}
	web.Redirect(w, r, projectURL(r.Context(), id))
	return nil
}

func (h handler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	var n int64
	err = h.tdb.Tx(r.Context(), func(tx tenant.Tx) (err error) {
		n, err = tx.DeleteProject(r.Context(), db.DeleteProjectParams{WorkspaceID: tx.WorkspaceID, ID: id})
		return err
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return web.ErrNotFound
	}
	web.Redirect(w, r, tenant.From(r.Context()).URL("/projects"))
	return nil
}
