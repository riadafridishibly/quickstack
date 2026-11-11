// Package insights is a workspace's overview: counts, charts drawn on the
// server, and the signed-in member's own open tasks.
package insights

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/chart"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/tenant"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const flowTarget = "flow"

var ranges = []int{7, 30, 90}

type handler struct{ tdb *tenant.DB }

func Routes(mux *http.ServeMux, tdb *tenant.DB) {
	h := handler{tdb: tdb}
	mux.Handle("GET /w/{ws}", web.Handle(h.overview))
}

type overviewView struct {
	Counts   db.TaskCountsRow
	Days     int
	Flow     chart.Data
	Assigned []db.OpenTasksByAssigneeRow
	Mine     []db.MyOpenTasksRow
}

func (h handler) overview(w http.ResponseWriter, r *http.Request) error {
	v := overviewView{Days: 30}
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && slices.Contains(ranges, d) {
		v.Days = d
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	since := today.AddDate(0, 0, 1-v.Days)
	fragment := web.HXTarget(r) == flowTarget
	err := h.tdb.Tx(r.Context(), func(tx tenant.Tx) error {
		flow, err := tx.DailyTaskFlow(r.Context(), db.DailyTaskFlowParams{WorkspaceID: tx.WorkspaceID, Since: since})
		if err != nil {
			return err
		}
		v.Flow = flowChart(flow)
		if fragment {
			return nil
		}
		if v.Counts, err = tx.TaskCounts(r.Context(), tx.WorkspaceID); err != nil {
			return err
		}
		if v.Assigned, err = tx.OpenTasksByAssignee(r.Context(), tx.WorkspaceID); err != nil {
			return err
		}
		v.Mine, err = tx.MyOpenTasks(r.Context(), db.MyOpenTasksParams{WorkspaceID: tx.WorkspaceID, AccountID: auth.From(r.Context()).ID})
		return err
	})
	if err != nil {
		return err
	}
	if fragment {
		return web.Render(w, r, http.StatusOK, flowSection(v))
	}
	return web.Render(w, r, http.StatusOK, overviewPage(v))
}

func flowChart(rows []db.DailyTaskFlowRow) chart.Data {
	d := chart.Data{
		Title:  "Tasks created and completed",
		Note:   "Per day, UTC",
		Empty:  "No tasks were created or completed in this period.",
		Kind:   chart.Lines,
		Format: chart.CountFormat,
	}
	created := make([]*float64, len(rows))
	completed := make([]*float64, len(rows))
	for i, r := range rows {
		d.Days = append(d.Days, r.Day)
		created[i], completed[i] = chart.Value(r.Created), chart.Value(r.Completed)
	}
	d.Series = []chart.Series{
		chart.WithValues(chart.Slot1, "Created", created),
		chart.WithValues(chart.Slot2, "Completed", completed),
	}
	return d
}
