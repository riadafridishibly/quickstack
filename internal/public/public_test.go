package public_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/riadafridishibly/quickstack/internal/apptest"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/tenant"
	"github.com/riadafridishibly/quickstack/internal/testdb"
)

func TestRoadmap(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	ws := testdb.CreateWorkspace(t, s.Pool, "acme")
	ctx := tenant.WithWorkspace(t.Context(), ws)
	err := tenant.NewDB(s.Pool).Tx(ctx, func(tx tenant.Tx) error {
		for _, p := range []struct {
			name   string
			public bool
		}{{"Open plans", true}, {"Secret plans", false}} {
			id, err := tx.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: tx.WorkspaceID, Name: p.name, Status: "active", Public: p.public})
			if err != nil {
				return err
			}
			if _, err := tx.CreateTask(ctx, db.CreateTaskParams{WorkspaceID: tx.WorkspaceID, ProjectID: id, Title: "Task of " + p.name}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	apptest.Status(t, s.Get("/p/acme", nil), http.StatusNotFound)
	if _, err := db.New(s.Pool).UpdateWorkspace(t.Context(), db.UpdateWorkspaceParams{ID: ws.ID, Name: "Acme", Slug: "acme", Public: true}); err != nil {
		t.Fatal(err)
	}
	w := s.Get("/p/acme", nil)
	apptest.Status(t, w, http.StatusOK)
	body := w.Body.String()
	if !strings.Contains(body, "Task of Open plans") || strings.Contains(body, "Secret") {
		t.Errorf("roadmap shows the wrong projects:\n%s", body)
	}
	if !strings.Contains(body, "src/public.css") {
		t.Error("roadmap doesn't use the public stylesheet")
	}
}

func TestLanding(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	apptest.Status(t, s.Get("/", nil), http.StatusOK)
	apptest.Status(t, s.Get("/nope", nil), http.StatusNotFound)
}
