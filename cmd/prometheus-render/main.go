// Command prometheus-render draws RRDtool/MRTG/Munin-style graphs from
// Prometheus or VictoriaMetrics.
//
// One PromQL query gives one image; the drawing is pure Go.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ExpTechTW/prometheus-render/internal/params"
	"github.com/ExpTechTW/prometheus-render/internal/promapi"
	"github.com/ExpTechTW/prometheus-render/internal/render"
	"github.com/ExpTechTW/prometheus-render/internal/server"
	"github.com/ExpTechTW/prometheus-render/tsgraph"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "prometheus-render: "+err.Error())
		os.Exit(1)
	}
}

func run(argv []string) error {
	fs := flag.NewFlagSet("prometheus-render", flag.ContinueOnError)
	fs.Usage = func() { _, _ = io.WriteString(os.Stderr, usage) }

	// Render settings are collected under the same keys the HTTP API uses, so
	// both front ends resolve them through the params package.
	opts := settings{values: url.Values{}}
	var (
		baseURL    = strFlag(fs, envOr("PROMETHEUS_URL", "http://localhost:9090"), "url", "u")
		user       = strFlag(fs, envOr("PROMETHEUS_AUTH", ""), "user")
		output     = strFlag(fs, "graph.png", "output", "o")
		serve      = strFlag(fs, "", "serve")
		headers    = map[string]string{}
		timeout    = fs.Duration("timeout", 30*time.Second, "")
		insecure   = boolFlag(fs, "insecure", "k")
		listThemes = boolFlag(fs, "list-themes")
		showVer    = boolFlag(fs, "version")
	)
	headerFlag(fs, headers, "header")

	opts.list(fs, "target", "query", "q")
	opts.list(fs, "legend", "legend", "l")
	opts.str(fs, "from", "-1h", "from", "start")
	opts.str(fs, "until", "now", "until", "end")
	opts.str(fs, "step", "", "step", "resolution")
	opts.str(fs, "maxPoints", "", "max-points")
	opts.str(fs, "theme", "mrtg", "theme", "t")
	opts.str(fs, "width", "400", "width", "w")
	opts.str(fs, "height", "175", "height", "H")
	opts.str(fs, "title", "", "title")
	opts.str(fs, "vtitle", "", "vtitle", "ylabel")
	opts.str(fs, "area", "", "area")
	opts.str(fs, "behindFrom", "", "behind-from")
	opts.str(fs, "lineWidth", "", "line-width")
	opts.str(fs, "yMin", "", "y-min")
	opts.str(fs, "yMax", "", "y-max")
	opts.str(fs, "base", "", "base")
	opts.str(fs, "zoom", "", "zoom")
	opts.str(fs, "tz", "", "tz")
	opts.flag(fs, "hideLegend", "hide-legend")
	opts.flag(fs, "hideStats", "hide-stats")

	if err := fs.Parse(argv); err != nil {
		return err
	}

	switch {
	case *showVer:
		fmt.Println("prometheus-render " + version)
		return nil
	case *listThemes:
		fmt.Println(strings.Join(tsgraph.ThemeNames(), "\n"))
		return nil
	}

	client := newClient(*baseURL, *timeout, *user, headers, *insecure)

	if *serve != "" {
		srv := &server.Server{Client: client, Defaults: params.Defaults{
			Theme: opts.values.Get("theme"),
			From:  opts.values.Get("from"),
			Until: opts.values.Get("until"),
		}}
		fmt.Fprintf(os.Stderr, "listening on %s  (try %s/render?target=up&from=-1h)\n",
			*serve, httpBase(*serve))
		return srv.ListenAndServe(*serve)
	}

	if len(opts.values["target"]) == 0 {
		fs.Usage()
		return errors.New("no -q/--query given")
	}

	g, err := params.Build(opts.values, params.Defaults{}, time.Now())
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	img, err := render.Draw(ctx, client, g)
	if err != nil {
		return err
	}
	return writeOutput(*output, img)
}

func newClient(baseURL string, timeout time.Duration, auth string, headers map[string]string, insecure bool) *promapi.Client {
	c := promapi.NewClient(baseURL, timeout)
	c.Headers = headers
	if auth != "" {
		c.Username, c.Password, _ = strings.Cut(auth, ":")
	}
	if insecure {
		c.HTTP.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // opt-in via --insecure
		}
	}
	return c
}

func writeOutput(path string, data []byte) error {
	if path == "-" {
		_, err := os.Stdout.Write(data)
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", path, len(data))
	return nil
}

func httpBase(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
