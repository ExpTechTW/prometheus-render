package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ExpTechTW/prometheus-render/internal/config"
	"github.com/ExpTechTW/prometheus-render/internal/server"
	"github.com/ExpTechTW/prometheus-render/internal/site"
)

// runSite draws the graphs described by a config file, on the timer that file
// asks for, and serves the result when it names an address.
func runSite(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}

	client := newClient(cfg.Source.URL, cfg.Source.Timeout.Duration(),
		cfg.Source.User, cfg.Source.Headers, cfg.Source.Insecure)
	// Drawing fans out over every graph and timescale at once; this keeps that
	// from reaching the source as a single burst.
	client.Limit = make(chan struct{}, cfg.Source.MaxQueries)

	logger := log.New(os.Stderr, "", log.LstdFlags)
	s := &site.Site{Cfg: cfg, Client: client, Log: logger}

	// A signal cancels the context, which lets the pass in flight finish the
	// file it is writing instead of being cut off mid-rename.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.Output.Listen == "" {
		return s.Run(ctx)
	}

	go func() {
		if err := s.Run(ctx); err != nil {
			logger.Printf("render: %v", err)
		}
	}()

	srv := &http.Server{
		Addr:              cfg.Output.Listen,
		Handler:           (&server.Server{Client: client, Dir: cfg.Output.Dir}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()

	logger.Printf("serving %s on %s", cfg.Output.Dir, httpBase(cfg.Output.Listen))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
