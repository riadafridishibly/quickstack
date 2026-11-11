// Command quickstack runs the server, and manages accounts from the
// command line.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/app"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/config"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/jobs"
	"github.com/riadafridishibly/quickstack/internal/mail"
)

const usage = `usage:
  quickstack                                    migrate, then run the server
  quickstack migrate                            apply migrations and exit
  quickstack create-user <email> <name>         create an account and print its password
  quickstack reset-password <email>             set a new random password and print it
  quickstack set-platform-role <email> <role>   role: superadmin, support or none`

func main() {
	var err error
	args := os.Args[1:]
	switch {
	case len(args) == 0:
		err = run()
	case len(args) == 1 && args[0] == "migrate":
		err = migrate()
	case len(args) == 3 && args[0] == "create-user":
		err = withPool(func(ctx context.Context, pool *pgxpool.Pool) error { return createUser(ctx, pool, args[1], args[2]) })
	case len(args) == 2 && args[0] == "reset-password":
		err = withPool(func(ctx context.Context, pool *pgxpool.Pool) error { return resetPassword(ctx, pool, args[1]) })
	case len(args) == 3 && args[0] == "set-platform-role":
		err = withPool(func(ctx context.Context, pool *pgxpool.Pool) error {
			return setPlatformRole(ctx, pool, args[1], args[2])
		})
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		slog.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := db.Migrate(ctx, cfg.MigrateURL); err != nil {
		return err
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	sender, err := mail.Open(cfg.SMTPURL, cfg.MailFrom)
	if err != nil {
		return err
	}
	var outbox *mail.Outbox
	if cfg.MailOutbox {
		outbox = &mail.Outbox{}
		sender = outbox
		slog.Info("mail goes to the outbox", "url", cfg.PublicURL+"/dev/mail")
	}
	jc, err := jobs.New(pool, app.Workers(pool, sender))
	if err != nil {
		return err
	}
	h, err := app.New(app.Config{
		ViteURL: cfg.ViteURL, TrustedProxies: cfg.TrustedProxies, PublicURL: cfg.PublicURL, Jobs: jc, Outbox: outbox,
	}, pool)
	if err != nil {
		return err
	}
	return app.Serve(ctx, cfg.Addr, cfg.DebugAddr, h, jc)
}

func migrate() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	return db.Migrate(context.Background(), cfg.MigrateURL)
}

func withPool(fn func(context.Context, *pgxpool.Pool) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := db.Migrate(ctx, cfg.MigrateURL); err != nil {
		return err
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return fn(ctx, pool)
}

func createUser(ctx context.Context, pool *pgxpool.Pool, email, name string) error {
	email = auth.NormalizeEmail(email)
	if !auth.ValidEmail(email) {
		return fmt.Errorf("%q is not an email address", email)
	}
	password := rand.Text()
	_, err := auth.CreateAccount(ctx, db.New(pool), email, name, password)
	if errors.Is(err, auth.ErrEmailTaken) {
		return fmt.Errorf("%s already has an account; use reset-password", email)
	}
	if err != nil {
		return err
	}
	fmt.Printf("Created %s with password: %s\n", email, password)
	return nil
}

func resetPassword(ctx context.Context, pool *pgxpool.Pool, email string) error {
	password, err := auth.ResetPassword(ctx, pool, email)
	if errors.Is(err, auth.ErrNoAccount) {
		return fmt.Errorf("no account with email %q", email)
	}
	if err != nil {
		return err
	}
	fmt.Printf("New password for %s: %s\nAll of this account's sessions were signed out.\n", auth.NormalizeEmail(email), password)
	return nil
}

func setPlatformRole(ctx context.Context, pool *pgxpool.Pool, email, role string) error {
	var r *string
	if role != "none" {
		if !auth.PlatformRole(role).Valid() {
			return fmt.Errorf("role %q: use superadmin, support or none", role)
		}
		r = &role
	}
	return pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		acc, err := q.GetAccountByEmail(ctx, auth.NormalizeEmail(email))
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("no account with email %q", email)
		}
		if err != nil {
			return err
		}
		if _, err := q.SetPlatformRole(ctx, db.SetPlatformRoleParams{ID: acc.ID, PlatformRole: r}); err != nil {
			return err
		}
		if err := q.RecordAudit(ctx, db.RecordAuditParams{Action: "platform.role_changed", Target: acc.Email + " → " + role + " (command line)"}); err != nil {
			return err
		}
		fmt.Printf("%s now has platform role %s\n", acc.Email, role)
		return nil
	})
}
