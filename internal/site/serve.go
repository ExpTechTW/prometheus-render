package site

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Handler serves the drawn site, and nothing else.
//
// There is deliberately no endpoint that accepts a query. Everything reachable
// here is a file the config asked to be drawn, so a request cannot widen what
// this process reads or how much work it costs. A parameter carrying PromQL
// would be exactly that: whoever can reach the page would choose the query,
// which is the shape of an injection rather than a feature.
//
// Nothing here sets cache headers. The site is redrawn on a timer at the same
// paths, so it does need a freshness policy -- but that belongs to whatever
// serves it to the world and knows its own edges.
func (s *Site) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	// More specific patterns win, so /healthz is not shadowed.
	mux.Handle("/", http.FileServer(http.Dir(s.Cfg.Output.Dir)))
	return mux
}

// ListenAndServe serves the site on addr until ctx is cancelled.
func (s *Site) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
