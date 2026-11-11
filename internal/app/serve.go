package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/riadafridishibly/quickstack/internal/jobs"
)

// Serve runs the HTTP server and job workers until ctx is cancelled, then
// shuts both down. A non-empty debugAddr also serves net/http/pprof there.
func Serve(ctx context.Context, addr, debugAddr string, h http.Handler, jc *jobs.Client) error {
	if err := jc.Start(context.WithoutCancel(ctx)); err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", addr)
		errc <- srv.ListenAndServe()
	}()
	var debug *http.Server
	if debugAddr != "" {
		debug = &http.Server{Addr: debugAddr, Handler: debugMux(), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			slog.Info("debug listening", "addr", debugAddr)
			// The debug listener is optional; failing to bind it doesn't
			// stop the server.
			if err := debug.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				slog.Error("debug listener", "addr", debugAddr, "err", err)
			}
		}()
	}

	var serveErr error
	select {
	case serveErr = <-errc:
	case <-ctx.Done():
		slog.Info("shutting down")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if debug != nil {
		_ = debug.Close()
	}
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		serveErr = errors.Join(serveErr, err)
	}
	if err := jc.Stop(shutdownCtx); err != nil {
		serveErr = errors.Join(serveErr, err)
	}
	return serveErr
}

func debugMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}
