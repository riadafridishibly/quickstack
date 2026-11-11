package projects_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/apptest"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/auth/authtest"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/tenant"
	"github.com/riadafridishibly/quickstack/internal/testdb"
)

func createProject(t *testing.T, pool *pgxpool.Pool, ws *tenant.Workspace, name string) int64 {
	t.Helper()
	ctx := tenant.WithWorkspace(t.Context(), ws)
	var id int64
	err := tenant.NewDB(pool).Tx(ctx, func(tx tenant.Tx) (err error) {
		id, err = tx.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: tx.WorkspaceID, Name: name, Status: "active"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func tasks(t *testing.T, pool *pgxpool.Pool, ws *tenant.Workspace, project int64) []db.ListTasksRow {
	t.Helper()
	ctx := tenant.WithWorkspace(t.Context(), ws)
	var rows []db.ListTasksRow
	err := tenant.NewDB(pool).Tx(ctx, func(tx tenant.Tx) (err error) {
		rows, err = tx.ListTasks(ctx, db.ListTasksParams{WorkspaceID: tx.WorkspaceID, ProjectID: project})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestList(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	ws := testdb.CreateWorkspace(t, s.Pool, "acme")
	_, c := authtest.Member(t, s.Pool, ws, "ana@example.com", auth.Viewer)
	createProject(t, s.Pool, ws, "Website relaunch")
	createProject(t, s.Pool, ws, "50% faster builds")

	page := s.Get("/w/acme/projects", c).Body.String()
	if !strings.Contains(page, "<html") || !strings.Contains(page, "Website relaunch") || strings.Contains(page, "New project") {
		t.Errorf("list page for a viewer:\n%s", page)
	}
	w := s.Do(apptest.Req{Path: "/w/acme/projects?q=50%25", Cookie: c, Target: "project-list"})
	apptest.Status(t, w, http.StatusOK)
	body := w.Body.String()
	if strings.Contains(body, "<html") || !strings.Contains(body, "50% faster builds") || strings.Contains(body, "Website relaunch") {
		t.Errorf("search fragment:\n%s", body)
	}
}

func TestCreateAndEdit(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	ws := testdb.CreateWorkspace(t, s.Pool, "acme")
	_, c := authtest.Member(t, s.Pool, ws, "ana@example.com", auth.Member)

	bad := url.Values{"name": {""}, "status": {"active"}}
	w := s.Do(apptest.Req{Path: "/w/acme/projects", Cookie: c, Form: bad, Target: "-"})
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	if strings.Contains(w.Body.String(), "<html") || !strings.Contains(w.Body.String(), "Enter a name.") {
		t.Errorf("htmx validation failure isn't the form alone:\n%s", w.Body)
	}
	w = s.Post("/w/acme/projects", c, bad)
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	if !strings.Contains(w.Body.String(), "<html") {
		t.Error("plain validation failure isn't a full page")
	}

	w = s.Do(apptest.Req{Path: "/w/acme/projects", Cookie: c, Target: "-", Form: url.Values{"name": {"Launch"}, "status": {"active"}}})
	apptest.Status(t, w, http.StatusOK)
	loc := w.Header().Get("HX-Redirect")
	if !strings.HasPrefix(loc, "/w/acme/projects/") {
		t.Fatalf("HX-Redirect = %q", loc)
	}
	apptest.Status(t, s.Get(loc, c), http.StatusOK)

	apptest.Redirect(t, s.Post(loc, c, url.Values{"name": {"Launch v2"}, "status": {"archived"}}), loc)
	if body := s.Get(loc, c).Body.String(); !strings.Contains(body, "Launch v2") || !strings.Contains(body, "Archived") {
		t.Error("edit didn't save")
	}
	apptest.Status(t, s.Post("/w/acme/projects/999999", c, url.Values{"name": {"x"}, "status": {"active"}}), http.StatusNotFound)
	apptest.Redirect(t, s.Post(loc+"/delete", c, url.Values{}), "/w/acme/projects")
	apptest.Status(t, s.Get(loc, c), http.StatusNotFound)
}

func TestTasks(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	ws := testdb.CreateWorkspace(t, s.Pool, "acme")
	ana, c := authtest.Member(t, s.Pool, ws, "ana@example.com", auth.Member)
	pid := createProject(t, s.Pool, ws, "Launch")
	base := fmt.Sprintf("/w/acme/projects/%d", pid)

	w := s.Do(apptest.Req{Path: base + "/tasks", Cookie: c, Target: "board", Form: url.Values{"title": {""}, "assignee": {"999"}}})
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	for _, msg := range []string{"Enter a title.", "Choose a member of the workspace."} {
		if !strings.Contains(w.Body.String(), msg) {
			t.Errorf("no %q", msg)
		}
	}
	for _, title := range []string{"First", "Second", "Third"} {
		w = s.Do(apptest.Req{Path: base + "/tasks", Cookie: c, Target: "board",
			Form: url.Values{"title": {title}, "assignee": {fmt.Sprint(ana)}, "due": {"2026-12-31"}}})
		apptest.Status(t, w, http.StatusOK)
	}
	if body := w.Body.String(); !strings.Contains(body, `<div id="board"`) || !strings.Contains(body, "<task-board") {
		t.Errorf("task create doesn't return the board:\n%s", body)
	}
	ts := tasks(t, s.Pool, ws, pid)
	if len(ts) != 3 || ts[0].Title != "First" || *ts[0].AssigneeName != "ana@example.com" {
		t.Fatalf("tasks %+v", ts)
	}

	// The island moves Third to the top of Doing, then Second above it,
	// and asks for no content back.
	minimal := http.Header{"Prefer": {"return=minimal"}}
	move := func(id int64, status string, order ...int64) int {
		ids := make([]string, len(order))
		for i, o := range order {
			ids[i] = fmt.Sprint(o)
		}
		return s.Do(apptest.Req{Path: fmt.Sprintf("%s/tasks/%d/move", base, id), Cookie: c, Header: minimal,
			Form: url.Values{"status": {status}, "order": {strings.Join(ids, ",")}}}).Code
	}
	first, second, third := ts[0].ID, ts[1].ID, ts[2].ID
	if code := move(third, "doing", third); code != http.StatusNoContent {
		t.Fatalf("move: %d", code)
	}
	if code := move(second, "doing", second, third); code != http.StatusNoContent {
		t.Fatalf("move: %d", code)
	}
	if code := move(first, "done"); code != http.StatusNoContent {
		t.Fatalf("move: %d", code)
	}
	var got []string
	for _, t := range tasks(t, s.Pool, ws, pid) {
		got = append(got, t.Status+":"+t.Title)
	}
	if want := "doing:Second doing:Third done:First"; strings.Join(got, " ") != want {
		t.Errorf("board = %v, want %s", got, want)
	}
	if code := move(first, "nope"); code != http.StatusBadRequest {
		t.Errorf("bad status: %d", code)
	}

	// Without JavaScript, the card's Move form posts and redirects back.
	apptest.Redirect(t, s.Post(fmt.Sprintf("%s/tasks/%d/move", base, first), c, url.Values{"status": {"todo"}}), base)
	apptest.Redirect(t, s.Post(fmt.Sprintf("%s/tasks/%d", base, first), c, url.Values{"title": {"First, edited"}}), base)
	apptest.Redirect(t, s.Post(fmt.Sprintf("%s/tasks/%d/delete", base, second), c, url.Values{}), base)
	if n := len(tasks(t, s.Pool, ws, pid)); n != 2 {
		t.Errorf("%d tasks after delete", n)
	}
}

func TestViewersAndOtherWorkspaces(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	acme := testdb.CreateWorkspace(t, s.Pool, "acme")
	globex := testdb.CreateWorkspace(t, s.Pool, "globex")
	_, viewer := authtest.Member(t, s.Pool, acme, "vic@example.com", auth.Viewer)
	ana, member := authtest.Member(t, s.Pool, acme, "ana@example.com", auth.Member)
	authtest.Join(t, s.Pool, globex, ana, auth.Member)
	pid := createProject(t, s.Pool, acme, "Launch")
	other := createProject(t, s.Pool, globex, "Secret")

	page := s.Get(fmt.Sprintf("/w/acme/projects/%d", pid), viewer).Body.String()
	if strings.Contains(page, "<task-board") || strings.Contains(page, "Add task") {
		t.Error("a viewer gets the editing board")
	}
	apptest.Status(t, s.Post(fmt.Sprintf("/w/acme/projects/%d/tasks", pid), viewer, url.Values{"title": {"x"}}), http.StatusForbidden)

	// globex's project id under acme's URL is not found, even for a member
	// of both.
	apptest.Status(t, s.Get(fmt.Sprintf("/w/acme/projects/%d", other), member), http.StatusNotFound)
	apptest.Status(t, s.Post(fmt.Sprintf("/w/acme/projects/%d/tasks", other), member, url.Values{"title": {"x"}}), http.StatusNotFound)
}
