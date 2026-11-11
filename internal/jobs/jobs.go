// Package jobs wraps the River client that runs background jobs from the
// PostgreSQL queue.
package jobs

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

const QueueEmail = "email"

type Client struct {
	*river.Client[pgx.Tx]
	working bool
}

// Setup is what the workers need: the workers themselves and the jobs River
// enqueues on a schedule, on the elected leader only.
type Setup struct {
	Workers  *river.Workers
	Periodic []*river.PeriodicJob
}

// New returns a client that inserts and works jobs, or only inserts them when
// s is nil; River refuses to start without at least one worker.
func New(pool *pgxpool.Pool, s *Setup) (*Client, error) {
	cfg := &river.Config{
		Logger: slog.Default(),
		// The app role doesn't own River's tables, so it can't REINDEX them.
		ReindexerIndexNames: []string{},
	}
	if s != nil {
		cfg.Workers = s.Workers
		cfg.PeriodicJobs = s.Periodic
		cfg.Queues = map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
			QueueEmail:         {MaxWorkers: 5},
		}
	}
	c, err := river.NewClient(riverpgxv5.New(pool), cfg)
	if err != nil {
		return nil, err
	}
	return &Client{Client: c, working: s != nil}, nil
}

func (c *Client) Start(ctx context.Context) error {
	if !c.working {
		return nil
	}
	return c.Client.Start(ctx)
}

func (c *Client) Stop(ctx context.Context) error {
	if !c.working {
		return nil
	}
	return c.Client.Stop(ctx)
}
