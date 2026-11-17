package workspaces

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/riadafridishibly/quickstack/internal/db"
)

// CleanupArgs is the daily job that deletes expired invitations.
type CleanupArgs struct{}

func (CleanupArgs) Kind() string { return "invitations_cleanup" }

type cleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	q *db.Queries
}

func (w *cleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	_, err := w.q.PurgeExpiredInvitations(ctx)
	return err
}

// AddWorkers registers the cleanup worker and returns its daily schedule.
func AddWorkers(w *river.Workers, pool *pgxpool.Pool) []*river.PeriodicJob {
	river.AddWorker(w, &cleanupWorker{q: db.New(pool)})
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour), func() (river.JobArgs, *river.InsertOpts) {
			return CleanupArgs{}, nil
		}, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}
