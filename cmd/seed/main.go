// Command seed fills the local database with sample data for development.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"

	"github.com/riadafridishibly/quickstack/internal/config"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/seed"
)

func main() {
	reset := flag.Bool("reset", false, "delete the sample data from an earlier run first")
	allowRemote := flag.Bool("allow-remote", false, "seed a database that isn't on this machine")
	flag.Parse()
	if err := run(*reset, *allowRemote); err != nil {
		slog.Error("seed", "err", err)
		os.Exit(1)
	}
}

func run(reset, allowRemote bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !allowRemote && !isLocal(cfg.DatabaseURL) {
		return fmt.Errorf("QUICKSTACK_DATABASE_URL isn't on this machine; pass --allow-remote to seed it anyway")
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
	err = seed.Run(ctx, pool, reset, func(msg string, args ...any) { slog.Info(msg, args...) })
	if err != nil {
		return err
	}
	fmt.Println("\nSign in at " + cfg.PublicURL + "/login as owner@seed.test (or admin@, manager@, member@, viewer@seed.test), password " + seed.Password)
	return nil
}

// isLocal reports whether the database URL points at this machine: a
// loopback address, localhost, or a Unix socket.
func isLocal(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" || host == "localhost" || (len(host) > 0 && host[0] == '/') {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
