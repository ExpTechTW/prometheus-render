# prometheus-render

Draw RRDtool / MRTG / Munin style graphs straight from **Prometheus** or
**VictoriaMetrics**.

繁體中文說明見 [README.md](README.md)。

```
prometheus-render -q 'node_load1' --from -6h -o load.png
```

![weekly traffic](out/traffic/core-1/light/peak/30m.png)

## Why this exists

There is no server-side image renderer in the Prometheus ecosystem. Both the
Prometheus UI and VictoriaMetrics' `vmui` draw with [uPlot], a browser canvas
library, so getting a PNG out of either means running a headless browser.

What makes the RRD look recognisable is specific: the Cur/Min/Avg/Max legend
table, the bevelled frame, a grey page around a white canvas, and gridlines
whose density follows how many seconds one pixel covers.

This repo carries [`tsgraph`](tsgraph/), a **pure-Go drawing library**. No
rrdtool process, no cairo, no cgo — `go build` cross-compiles it to any
platform as a single binary.

[uPlot]: https://github.com/leeoniya/uPlot

## Features

**The four MRTG timescales**, named after their averaging interval, with spans
that abut without a gap:

| File | Averaging | Span | MRTG calls it |
|---|---|---|---|
| `5m` | 5 minutes | 32 hours | Daily |
| `30m` | 30 minutes | 8 days | Weekly |
| `2h` | 2 hours | 5 weeks | Monthly |
| `1d` | 1 day | 13 months | Yearly |

**MRTG's four colour roles.** The peaks are the point of the scheme, because
averaging hides spikes:

| Colour | | Series |
|---|---|---|
| `#00CC00` | green | RX, filled |
| `#0000FF` | blue | TX, line |
| `#006600` | dark green | peak of the RX |
| `#FF00FF` | magenta | peak of the TX |

In the graph above the 30-minute average tops out at 41 Mbps, while the busiest
5-minute sample in the same window reached **68 Mbps** — the average understates
the real peak by two thirds.

**The dark theme** keeps MRTG's green and blue and lightens the two peak colours
instead: on a dark canvas a peak has to sit lighter than its own average to
separate from it, the opposite of darker-is-peak on white.

![dark theme](out/traffic/edge-1/dark/peak/30m.png)

**Layering.** The grid and the labelled time rules are drawn *over* the data,
dashed and blended — an opaque line over a filled area cuts it into bands. A red
rule marks a tick that carries a label, so it never lands where there is nothing
to read.

**Real resolution.** `--zoom` redraws rather than upscales. The canvas, the
font, the line widths, the dash lengths and the grid thickness all scale
together; missing any one of them shows up as a washed-out colour rather than as
a thin line.

**A gap is a gap.** NaN breaks the line instead of being drawn as zero.

![yearly](out/rps/edge-1/light/peak/1d.png)

## Install

```sh
make build          # -> bin/prometheus-render
make install        # -> $GOPATH/bin
make test
```

Nothing else to install.

## Usage

```sh
prometheus-render -q <promql> [flags]
prometheus-render --config site.yml --serve :8080
```

Run `prometheus-render -h` for the full list.

| Flag | Meaning |
|---|---|
| `-u, --url` | Data source base URL (env `PROMETHEUS_URL`), default `http://localhost:9090` |
| `-q, --query` | PromQL expression, repeatable |
| `-l, --legend` | Series name, with `{{label}}` placeholders, repeatable |
| `--from` / `--until` | Window: `-1h`, `-7d`, `now-90min`, a Unix timestamp, RFC3339 |
| `--step` | Resolution (`60`, `5min`). Default: about one point per pixel |
| `-t, --theme` | `mrtg`, `dark`, `munin` |
| `-w/-H` | Plot canvas size, default 400x175 |
| `--area` | `none`, `first`, `all`, `stacked` |
| `--zoom` | Redraw at a multiple of the nominal size |
| `--behind-from` | Draw series N onwards first, i.e. behind the rest |
| `--tz` | Timezone, e.g. `Asia/Taipei` |
| `-o, --output` | Output file, `-` for stdout |

### VictoriaMetrics

Point `--url` at the vmselect Prometheus-compatible prefix:

```sh
prometheus-render -u http://vmselect:8481/select/0/prometheus -q 'node_load1'
```

### Examples

The classic MRTG traffic graph — filled RX, TX as a line:

```sh
prometheus-render -t mrtg --area first --vtitle 'Mbps' \
  -q 'rate(node_network_receive_bytes_total{device="eth0"}[5m])*8/1000000'  -l 'rx' \
  -q 'rate(node_network_transmit_bytes_total{device="eth0"}[5m])*8/1000000' -l 'tx' \
  --title 'eth0' -o traffic.png
```

Munin-style stacked CPU:

```sh
prometheus-render -t munin --area stacked --from -1d --title CPU \
  -q 'sum by (mode) (rate(node_cpu_seconds_total[5m]))' -l '{{mode}}' -o cpu.png
```

### Scheduled graphs and an HTML site

`--config` takes a YAML file, draws every graph in it over MRTG's four
timescales, writes the pages that present them, and redraws on the interval
the file names:

```bash
prometheus-render --config site.yml
```

```yaml
source:
  url: http://localhost:9090

output:
  dir: site
  title: Network
  listen: ":8080"   # empty writes the files and nothing more, for nginx
  interval: 5m      # 0 draws once and exits, which is what cron wants
  workers: 0        # 0 means one per CPU

defaults:
  theme: mrtg
  dark_theme: dark  # switched on the page; both share one query
  peak: true        # MRTG's peak traces, offered as a button
  area: first
  tz: Asia/Taipei
  # Omit ranges to get MRTG's four: 1d / 1w / 1m / 1y

graphs:
  - name: traffic
    title: Traffic
    vtitle: Mbps
    series:
      - {expr: 'sum(rate(nginx_http_in_bytes_total[5m])) * 8 / 1e6', legend: RX}
      - {expr: 'sum(rate(nginx_http_bytes_total[5m])) * 8 / 1e6',    legend: TX}
```

A full example is in [`site.example.yml`](site.example.yml).

#### Many places

A `regions` block makes the same graphs readable both ways round: **everything
about one place**, or **one thing across every place**.

```yaml
regions:
  label: region              # split on this; it also names the placeholder
  match: '{job="nginx"}'     # discover values from these series only
  titles: {tnn: core-tnn1, tyo: core-tyo1}   # used in pages, URLs and captions

graphs:
  - name: traffic
    series:
      - {expr: 'sum(rate(nginx_http_in_bytes_total{region="$region"}[5m]))', legend: RX}

  - name: sensor
    only_regions: [tnn]      # something that lives in one place

  - name: total
    global: true             # not split at all
```

The name under `titles` is what the site calls a region everywhere -- pages,
URLs and the captions on the drawings -- while the label value stays in the
query. Because it becomes a path segment, a name is letters, digits, dot, dash
or underscore; anything else is refused at load rather than quietly becoming
something else in the URL.

Redraws are pinned to the clock: a five-minute interval draws at :00, :05,
:10, rather than from whenever the process started. A page works out the same
boundaries from the same interval, so neither side has to ask the other --
**no version file, no polling**. The page is given one number: the interval.

Each pass begins **five seconds early**, so the drawings are in place when the
boundary arrives. A pass that outruns that lead says so in the log.

The header counts down to the next boundary and swaps the drawings there.
**Image URLs are stable** (no version query), so every cache in front of them
keeps hitting.

The inline scripts carry `data-cfasync="false"` so Cloudflare's Rocket Loader
leaves them alone: it rewrites the type attribute to something the browser will
not run and executes the script itself, later and out of order, which leaves the
page without its timer, its toggles or its theme.

Neither a new element nor a fetch can do this on its own. A browser keeps a
second cache for images, above HTTP and keyed by URL, which deliberately
ignores expiry -- the HTML spec calls it the list of available images, and it
exists for compatibility. An `<img>` pointed at a URL it already holds renders
from there without a request, while a fetch of that URL fills the HTTP cache,
which is a different store.

So the bytes are handed to the element directly: `fetch` → `blob` →
`createObjectURL`. That sidesteps the URL-keyed entry while **the address on
the wire never changes**, so nginx and the CDN keep their entries and their hit
rate.

**Every drawing arrives that way, including the first** -- no `<img>` in the
HTML carries a `src` -- so a page just opened cannot be showing a stale copy
either.

Each drawing carries the time it was made in its bottom right corner, in the
same zone -- so a picture that has been saved or passed on still says how old
it is.

**The values are discovered from the data**, so a node arriving or leaving
changes the pages without editing the file. A graph with nothing to show in a
region is left off the pages; one whose query failed is kept, because that
means unknown rather than absent.

A label value that could reshape a query -- one carrying a quote, a backslash
or a brace -- is refused rather than escaped. Real infrastructure labels do not
look like that.

#### Themes and peaks

Every graph is drawn in a light and a dark palette, and a graph with
`peak: true` is also drawn with MRTG's peak traces. The page switches between
them with buttons; the theme is remembered in `localStorage`, and each drawing
remembers its own peak setting.

Peaks follow MRTG: the highest value in each sample bucket rather than the
mean, drawn behind the averages so they show only where they rise above them.
**Each timescale peaks at the resolution of the one below it** -- the yearly
graph at the monthly graph's -- which is both what MRTG does and what keeps the
subquery from asking for a hundred thousand points.

One query feeds all four images: a palette and a peak trace change how samples
are drawn, not which are read.

### Serving the drawn pages

`--serve` serves the pages that were drawn, overriding `output.listen` in the
config. It requires `--config`:

```bash
prometheus-render --config site.yml --serve :8080
```

**What is served is what the config drew; no endpoint accepts a query.** That
is deliberate. A URL parameter carrying PromQL would let whoever can reach the
page decide what this process reads and how much it costs to read -- an
injection surface rather than a feature. To publish another graph, add another
entry under `graphs`.

`/healthz` returns `ok`.

## Using it as a library

`tsgraph` stands on its own and knows nothing about Prometheus — it takes
samples and returns a PNG:

```go
import "github.com/ExpTechTW/prometheus-render/tsgraph"

theme := tsgraph.LookupTheme("mrtg")
png, err := tsgraph.Render([]tsgraph.Series{{
    Name:   "rx",
    Start:  start.Unix(),
    Step:   300,
    Values: values,           // NaN marks a gap
    Colour: theme.Colour(0),
    Kind:   tsgraph.Area,
}}, tsgraph.Options{
    Title: "eth0", VLabel: "Mbps",
    Width: 500, Height: 150, Theme: theme,
})
```

Its only dependency is `golang.org/x/image`. The axis algorithms are described
in [`tsgraph/DESIGN.md`](tsgraph/DESIGN.md).

## Example images

Everything under `out/` is rendered from [`testdata/sample.db`](testdata/) —
offline, reproducible, and needing no data source:

```sh
make examples
```

The layout is `out/<metric>/<host>/<theme>/<variant>/<tier>.png`, where the
variant is `peak` (averages plus the peak traces) or `plain` (averages alone).

## Notes

- **Time ranges** accept the rrdtool spellings: `-1h`, `-90min`, `-7d`, `-2w`,
  `now-1d`, plus Unix timestamps and RFC3339.
- **Sample ceiling** is 11000 points per query, the Prometheus limit. Past that
  the step widens rather than the query failing.
- **Bucket convention**, as in RRD and MRTG: a sample taken at time T is drawn
  in the bucket *ending* at T.

## Layout

```
tsgraph/                the drawing library, usable on its own
cmd/prometheus-render   CLI
internal/promapi        query_range client, time parsing, densifying
internal/query          window and step resolution, parallel fetch
internal/params         settings shared by the CLI and the config
internal/render         joins a query to the library
internal/config         the YAML config file
internal/site           scheduling, the worker pool and the HTML
examples/gallery        renders out/ from SQLite (its own module)
testdata/sample.db      the sample dataset
hack/                   checks that read the rendered pixels back
```

## License

Apache 2.0. See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
