package main

const usage = `prometheus-render - RRDtool/MRTG/Munin-style graphs from Prometheus or VictoriaMetrics

Drawing is pure Go: no rrdtool, no cairo, no cgo. One query gives one image; a
config file gives a whole site, redrawn on a timer and presented as plain HTML.

USAGE
  prometheus-render -q <promql> [flags]
  prometheus-render --serve :8080 [flags]

DATA SOURCE
  -u, --url URL        Prometheus/VictoriaMetrics base URL  (env PROMETHEUS_URL)
                       default http://localhost:9090
                       VictoriaMetrics: http://vmselect:8481/select/0/prometheus
      --user u:p       HTTP basic auth                      (env PROMETHEUS_AUTH)
      --header 'K: V'  Extra request header (repeatable)
      --timeout D      Request timeout (default 30s)
  -k, --insecure       Skip TLS certificate verification

QUERY
  -q, --query EXPR     PromQL expression (repeatable)
  -l, --legend FMT     Series name; {{label}} placeholders, e.g. '{{instance}}'
                       (repeatable, matched to -q by position; one applies to all)
      --from T         Window start (default -1h). -1h, -7d, now-90min,
                       1735689600, 2026-01-01T00:00:00Z
      --until T        Window end (default now)
      --step D         Resolution, e.g. 60, 5min. Default: ~1 point per pixel
      --max-points N   Sample ceiling per query (default 11000)

OUTPUT
  -o, --output PATH    Output PNG, '-' for stdout (default graph.png)
  -t, --theme NAME     mrtg|munin|dark (default mrtg)
      --list-themes

APPEARANCE
  -w, --width N        Plot canvas width, default 400. The canvas is measured
  -H, --height N       the way rrdtool measures it, so the file comes out
                       larger than these numbers.
      --title S            --vtitle S (y-axis label)
      --area MODE      none|first|all|stacked
      --behind-from N  Draw series N onwards first, i.e. behind the rest
      --line-width N
      --y-min V            --y-max V        (both given implies a rigid scale)
      --base 1024      Binary units for the y-axis and the statistics
      --hide-stats     Drop the Cur/Min/Avg/Max table
      --hide-legend
      --zoom N         Scale the whole image (2 for HiDPI)
      --tz ZONE        Time axis timezone, e.g. Asia/Taipei

SERVER
      --serve ADDR     Serve GET /render?target=<promql>&from=-1h&width=400
                       Most flags above are accepted as URL parameters.

EXAMPLES
  # Classic MRTG traffic graph
  prometheus-render -t mrtg --area first --vtitle 'bits/sec' \
    -q 'rate(node_network_receive_bytes_total{device="eth0"}[5m])*8'  -l 'inbound ' \
    -q 'rate(node_network_transmit_bytes_total{device="eth0"}[5m])*8' -l 'outbound' \
    --title 'eth0 traffic' -o traffic.png

  # Munin-style stacked CPU, last 24 hours
  prometheus-render -t munin --area stacked --from -1d \
    -q 'sum by (mode) (rate(node_cpu_seconds_total[5m]))' -l '{{mode}}' \
    --title 'CPU' -o cpu.png

  # A whole site, redrawn every 5 minutes and served on :8080
`
