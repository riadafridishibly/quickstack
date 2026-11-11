package insights_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/riadafridishibly/quickstack/internal/apptest"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/auth/authtest"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/tenant"
	"github.com/riadafridishibly/quickstack/internal/testdb"
)

func TestOverview(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	ws := testdb.CreateWorkspace(t, s.Pool, "acme")
	ana, c := authtest.Member(t, s.Pool, ws, "ana@example.com", auth.Member)
	ctx := tenant.WithWorkspace(t.Context(), ws)
	err := tenant.NewDB(s.Pool).Tx(ctx, func(tx tenant.Tx) error {
		pid, err := tx.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: tx.WorkspaceID, Name: "Launch", Status: "active"})
		if err != nil {
			return err
		}
		yesterday := time.Now().AddDate(0, 0, -1)
		for i, st := range []string{"todo", "doing", "done"} {
			var done *time.Time
			if st == "done" {
				done = &yesterday
			}
			_, err := tx.SeedTask(ctx, db.SeedTaskParams{WorkspaceID: tx.WorkspaceID, ProjectID: pid, Title: "Task " + st,
				Status: st, Position: int32(i), AssigneeID: &ana, DueOn: &yesterday, CreatedAt: yesterday, CompletedAt: done})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	w := s.Get("/w/acme", c)
	apptest.Status(t, w, http.StatusOK)
	body := w.Body.String()
	// Ana's open tasks are listed as hers; the done one isn't.
	for _, want := range []string{"Done this week", ">Task todo<", ">Task doing<", "<svg"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	if strings.Contains(body, ">Task done<") {
		t.Error("a done task is listed as assigned to you")
	}

	w = s.Do(apptest.Req{Path: "/w/acme?days=7", Cookie: c, Target: "flow"})
	apptest.Status(t, w, http.StatusOK)
	if b := w.Body.String(); !strings.HasPrefix(b, `<div id="flow"`) || !strings.Contains(b, `aria-current="page">7 days`) {
		t.Errorf("flow fragment:\n%.500s", b)
	}
}
