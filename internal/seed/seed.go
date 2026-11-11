// Package seed fills a development database with sample accounts,
// workspaces, projects and 90 days of tasks, through the app's own write
// functions where it can.
package seed

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/tenant"
	"github.com/riadafridishibly/quickstack/internal/workspaces"
)

// Password is every seeded account's password.
const Password = "seed-password"

var ErrSeeded = errors.New("seed: the sample data is already there; run with --reset to replace it")

type person struct {
	Email, Name string
	Platform    auth.PlatformRole
}

var people = []person{
	{"admin@seed.test", "Ada Admin", auth.Superadmin},
	{"support@seed.test", "Sam Support", auth.Support},
	{"owner@seed.test", "Olivia Owner", ""},
	{"manager@seed.test", "Arjun Manager", ""},
	{"member@seed.test", "Mia Member", ""},
	{"viewer@seed.test", "Victor Viewer", ""},
	{"noah@seed.test", "Noah Park", ""},
	{"lena@seed.test", "Lena Okafor", ""},
}

type space struct {
	Slug, Name string
	Public     bool
	Members    map[string]auth.Role
	Projects   []project
}

type project struct {
	Name, Description string
	Public, Archived  bool
	Tasks             int
}

var spaces = []space{
	{
		Slug: "acme", Name: "Acme Robotics", Public: true,
		Members: map[string]auth.Role{
			"manager@seed.test": auth.Admin, "member@seed.test": auth.Member, "viewer@seed.test": auth.Viewer,
			"noah@seed.test": auth.Member, "lena@seed.test": auth.Member,
		},
		Projects: []project{
			{Name: "Website relaunch", Description: "New marketing site, docs and pricing page.", Public: true, Tasks: 38},
			{Name: "Mobile app 2.0", Description: "Offline mode, push notifications and a new onboarding flow.", Public: true, Tasks: 44},
			{Name: "Warehouse robots", Description: "Firmware and fleet tooling for the pilot warehouse.", Tasks: 52},
			{Name: "Hiring", Description: "Open roles for the spring.", Archived: true, Tasks: 12},
		},
	},
	{
		Slug: "globex", Name: "Globex",
		Members: map[string]auth.Role{"member@seed.test": auth.Admin, "lena@seed.test": auth.Viewer},
		Projects: []project{
			{Name: "Q1 planning", Description: "Goals, budget and headcount.", Tasks: 18},
		},
	},
}

var verbs = []string{"Draft", "Review", "Ship", "Fix", "Design", "Test", "Document", "Plan", "Measure", "Clean up"}
var nouns = []string{
	"the pricing page", "sign-up emails", "the settings screen", "the release notes", "error tracking",
	"the onboarding checklist", "CSV export", "the search index", "battery telemetry", "the status page",
	"dark mode", "the API rate limits", "push notification copy", "the support macros", "the backup job",
}

// Run seeds the database. With reset, it first deletes the sample data a
// previous run left.
func Run(ctx context.Context, pool *pgxpool.Pool, reset bool, log func(string, ...any)) error {
	q := db.New(pool)
	if reset {
		for _, s := range spaces {
			if err := q.SeedDeleteWorkspace(ctx, s.Slug); err != nil {
				return err
			}
		}
		for _, p := range people {
			if err := q.SeedDeleteAccount(ctx, p.Email); err != nil {
				return err
			}
		}
	} else if _, err := q.GetAccountByEmail(ctx, people[0].Email); err == nil {
		return ErrSeeded
	}

	rng := rand.New(rand.NewPCG(2026, 11))
	now := time.Now().UTC()
	ids := map[string]int64{}
	err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		for i, p := range people {
			acc, err := auth.CreateAccount(ctx, q, p.Email, p.Name, Password)
			if err != nil {
				return fmt.Errorf("%s: %w", p.Email, err)
			}
			ids[p.Email] = acc.ID
			if p.Platform != "" {
				r := string(p.Platform)
				if _, err := q.SetPlatformRole(ctx, db.SetPlatformRoleParams{ID: acc.ID, PlatformRole: &r}); err != nil {
					return err
				}
			}
			joined := now.AddDate(0, 0, -60+i*7).Add(-time.Duration(rng.IntN(20)) * time.Hour)
			if err := q.SeedAccountCreatedAt(ctx, db.SeedAccountCreatedAtParams{ID: acc.ID, CreatedAt: joined}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	log("accounts", "count", len(people), "password", Password)

	tdb := tenant.NewDB(pool)
	for i, s := range spaces {
		var ws db.Workspace
		err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) (err error) {
			q := db.New(tx)
			if ws, err = workspaces.Create(ctx, q, s.Name, s.Slug, ids["owner@seed.test"]); err != nil {
				return err
			}
			if _, err := q.UpdateWorkspace(ctx, db.UpdateWorkspaceParams{ID: ws.ID, Name: ws.Name, Slug: ws.Slug, Public: s.Public}); err != nil {
				return err
			}
			if err := q.SeedWorkspaceCreatedAt(ctx, db.SeedWorkspaceCreatedAtParams{ID: ws.ID, CreatedAt: now.AddDate(0, 0, -55+i*20)}); err != nil {
				return err
			}
			for email, role := range s.Members {
				if err := q.AddMember(ctx, db.AddMemberParams{WorkspaceID: ws.ID, AccountID: ids[email], Role: string(role)}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("workspace %s: %w", s.Slug, err)
		}
		assignees := []int64{ids["owner@seed.test"]}
		for email, role := range s.Members {
			if role != auth.Viewer {
				assignees = append(assignees, ids[email])
			}
		}
		ctx := tenant.WithWorkspace(ctx, &tenant.Workspace{ID: ws.ID, Slug: ws.Slug, Name: ws.Name})
		tasks := 0
		err = tdb.Tx(ctx, func(tx tenant.Tx) error {
			for _, p := range s.Projects {
				n, err := seedProject(ctx, tx, rng, now, p, assignees)
				if err != nil {
					return fmt.Errorf("project %s: %w", p.Name, err)
				}
				tasks += n
			}
			return nil
		})
		if err != nil {
			return err
		}
		log("workspace", "url", "/w/"+s.Slug, "projects", len(s.Projects), "tasks", tasks)
	}
	return nil
}

func seedProject(ctx context.Context, tx tenant.Tx, rng *rand.Rand, now time.Time, p project, assignees []int64) (int, error) {
	status := "active"
	if p.Archived {
		status = "archived"
	}
	pid, err := tx.CreateProject(ctx, db.CreateProjectParams{
		WorkspaceID: tx.WorkspaceID, Name: p.Name, Description: p.Description, Status: status, Public: p.Public,
	})
	if err != nil {
		return 0, err
	}
	position := map[string]int32{}
	for range p.Tasks {
		created := now.Add(-time.Duration(rng.Float64() * 90 * 24 * float64(time.Hour)))
		st := "todo"
		switch x := rng.Float64(); {
		case x < 0.55:
			st = "done"
		case x < 0.72:
			st = "doing"
		}
		var completed *time.Time
		if st == "done" {
			c := created.Add(time.Duration(1+rng.IntN(14*24)) * time.Hour)
			if c.After(now) {
				c = now.Add(-time.Duration(rng.IntN(48)) * time.Hour)
			}
			if c.Before(created) {
				c = created
			}
			completed = &c
		}
		var assignee *int64
		if rng.Float64() < 0.8 {
			a := assignees[rng.IntN(len(assignees))]
			assignee = &a
		}
		var due *time.Time
		if st != "done" && rng.Float64() < 0.6 {
			d := now.AddDate(0, 0, rng.IntN(40)-12).Truncate(24 * time.Hour)
			due = &d
		}
		position[st]++
		_, err := tx.SeedTask(ctx, db.SeedTaskParams{
			WorkspaceID: tx.WorkspaceID, ProjectID: pid,
			Title:  verbs[rng.IntN(len(verbs))] + " " + nouns[rng.IntN(len(nouns))],
			Status: st, Position: position[st], AssigneeID: assignee, DueOn: due,
			CreatedAt: created, CompletedAt: completed,
		})
		if err != nil {
			return 0, err
		}
	}
	return p.Tasks, nil
}
