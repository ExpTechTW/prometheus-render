package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ExpTechTW/prometheus-render/internal/config"
	"github.com/ExpTechTW/prometheus-render/internal/site"
)

// runSite draws the graphs a config file names, on the timer it asks for, and
// serves the result when it names an address. listen overrides the address in
// the file.
func runSite(path, listen string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if listen != "" {
		cfg.Output.Listen = listen
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

	logger.Printf("serving %s on %s", cfg.Output.Dir, httpBase(cfg.Output.Listen))
	return s.ListenAndServe(ctx, cfg.Output.Listen)
}
