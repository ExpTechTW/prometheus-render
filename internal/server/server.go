// Package server exposes a /render endpoint that draws a PromQL query as an
// RRD-style image, so a dashboard or an <img> tag can point straight at it.
package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ExpTechTW/prometheus-render/internal/params"
	"github.com/ExpTechTW/prometheus-render/internal/promapi"
	"github.com/ExpTechTW/prometheus-render/internal/render"
)

// Server answers /render requests. Parameters are interpreted by the params
// package, the same one the CLI uses.
type Server struct {
	Client   *promapi.Client
	Defaults params.Defaults

	// Dir, when set, serves a generated site at / alongside the live
	// endpoint, so the scheduled pages and an ad-hoc /render?target=... are
	// reachable from one address.
	Dir string
}

// ListenAndServe starts the HTTP server on addr.
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

// Handler returns the router, so the server can be embedded elsewhere.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/render", s.handleRender)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	if s.Dir != "" {
		// The patterns above are more specific, so they still win.
		mux.Handle("/", http.FileServer(http.Dir(s.Dir)))
	}
	return mux
}

func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	g, err := params.Build(r.Form, s.Defaults, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	img, err := render.Draw(ctx, s.Client, g)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(img)))
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(img)
}
