# prometheus-render

從 **Prometheus** 或 **VictoriaMetrics** 直接畫出 RRDtool / MRTG / Munin 風格的圖。

```
prometheus-render -q 'node_load1' --from -6h -o load.png
```

![週尺度流量圖](out/traffic/core-1/light/peak/30m.png)

## 為什麼需要這個

Prometheus 生態沒有伺服器端的圖片產生器。Prometheus 自己的 UI 和 VictoriaMetrics 的
`vmui` 都用 [uPlot] 在瀏覽器 canvas 上畫，要拿到 PNG 就得跑一個無頭瀏覽器。

而 RRD 那個外觀之所以好認，是幾個很具體的東西：Cur/Min/Avg/Max 統計表、立體外框、
灰底配白畫布、以及依「一個像素涵蓋多少秒」自動決定的格線與標籤密度。

這個 repo 帶了 [`tsgraph`](tsgraph/)——**純 Go 的繪圖函式庫**。不需要 rrdtool
行程、不需要 cairo、沒有 cgo，`go build` 就能交叉編譯到任何平台，單一執行檔。

[uPlot]: https://github.com/leeoniya/uPlot

## 特色

**四層 MRTG 時間尺度**，依平均間隔命名，跨度剛好接續不留空隙：

| 檔名 | 平均間隔 | 跨度 | MRTG 稱呼 |
|---|---|---|---|
| `5m` | 5 分鐘 | 32 小時 | Daily |
| `30m` | 30 分鐘 | 8 天 | Weekly |
| `2h` | 2 小時 | 5 週 | Monthly |
| `1d` | 1 天 | 13 個月 | Yearly |

**MRTG 的四色語意**，peak 是它存在的理由——平均會吃掉尖峰：

| 顏色 | | 序列 |
|---|---|---|
| `#00CC00` | 綠 | 進站，填充 |
| `#0000FF` | 藍 | 出站，線條 |
| `#006600` | 墨綠 | 進站的 peak |
| `#FF00FF` | 洋紅 | 出站的 peak |

上圖裡 30 分鐘平均最高 41 Mbps，但同一段時間內最忙的 5 分鐘樣本達 **68 Mbps**
——平均低估了三分之二。

**深色主題**保留 MRTG 的綠與藍，只把兩個 peak 色調亮：深底上 peak 必須比本體亮才
分得出來，與白底「peak 更暗」相反。

![深色主題](out/traffic/edge-1/dark/peak/30m.png)

**繪製層級**：時間刻度線與網格畫在資料**上方**，且半透明加虛線——不透明的線會把填充
區切成橫條。紅色虛線只出現在有標籤的刻度，不會落在讀不出東西的位置。

**高解析度**：`--zoom` 是真的重繪而非放大。畫布、字型、線寬、虛線長度、格線厚度會
一起縮放；漏掉任何一項都會以「顏色偏淡」的形式浮現，而不是「線變細」。

**缺值就是缺值**：NaN 畫成斷線，不會被當成 0。

![年尺度](out/rps/edge-1/light/peak/1d.png)

## 安裝

```sh
make build          # -> bin/prometheus-render
make install        # -> $GOPATH/bin
make test
```

不需要安裝其他東西。

## 用法

```sh
prometheus-render -q <promql> [flags]
prometheus-render --serve :8080 [flags]
```

完整旗標見 `prometheus-render -h`。

| 旗標 | 說明 |
|---|---|
| `-u, --url` | 資料源位址（env `PROMETHEUS_URL`），預設 `http://localhost:9090` |
| `-q, --query` | PromQL 運算式，可重複 |
| `-l, --legend` | 序列名稱，支援 `{{label}}` 佔位符，可重複 |
| `--from` / `--until` | 時間窗，如 `-1h`、`-7d`、`now-90min`、Unix 時間戳、RFC3339 |
| `--step` | 取樣間隔（`60`、`5min`）。預設約一像素一點 |
| `-t, --theme` | `mrtg`、`dark`、`munin` |
| `-w/-H` | 繪圖區尺寸，預設 400x175 |
| `--area` | `none`、`first`、`all`、`stacked` |
| `--zoom` | 以倍數重繪 |
| `--behind-from` | 第 N 條之後先畫，也就是畫在下層 |
| `--tz` | 時區，例如 `Asia/Taipei` |
| `-o, --output` | 輸出檔，`-` 表示 stdout |

### VictoriaMetrics

`--url` 指向 vmselect 的 Prometheus 相容前綴：

```sh
prometheus-render -u http://vmselect:8481/select/0/prometheus -q 'node_load1'
```

### 範例

經典 MRTG 流量圖——進站填充、出站線條：

```sh
prometheus-render -t mrtg --area first --vtitle 'Mbps' \
  -q 'rate(node_network_receive_bytes_total{device="eth0"}[5m])*8/1000000'  -l 'rx' \
  -q 'rate(node_network_transmit_bytes_total{device="eth0"}[5m])*8/1000000' -l 'tx' \
  --title 'eth0' -o traffic.png
```

Munin 風格的堆疊 CPU：

```sh
prometheus-render -t munin --area stacked --from -1d --title CPU \
  -q 'sum by (mode) (rate(node_cpu_seconds_total[5m]))' -l '{{mode}}' -o cpu.png
```

### HTTP 服務

`--serve` 開一個 `/render` 端點，可以直接讓 `<img>` 指過來。上面的旗標都能當
URL 參數用，`target` 對應 `--query`：

```html
<img src="http://localhost:8080/render?target=node_load1&from=-6h&theme=dark">
```

`/healthz` 回 `ok`。

## 當成函式庫用

`tsgraph` 可以獨立使用，它不知道 Prometheus 的存在——收樣本，回傳 PNG：

```go
import "github.com/ExpTechTW/prometheus-render/tsgraph"

theme := tsgraph.LookupTheme("mrtg")
png, err := tsgraph.Render([]tsgraph.Series{{
    Name:   "rx",
    Start:  start.Unix(),
    Step:   300,
    Values: values,           // NaN 表示缺值
    Colour: theme.Colour(0),
    Kind:   tsgraph.Area,
}}, tsgraph.Options{
    Title: "eth0", VLabel: "Mbps",
    Width: 500, Height: 150, Theme: theme,
})
```

唯一的依賴是 `golang.org/x/image`。座標軸演算法的設計說明在
[`tsgraph/DESIGN.md`](tsgraph/DESIGN.md)。

## 範例圖片

`out/` 底下的圖全部由 [`testdata/sample.db`](testdata/) 產生，離線、可重現、
不需要任何資料源：

```sh
make examples
```

結構是 `out/<指標>/<主機>/<主題>/<版本>/<尺度>.png`，其中版本是 `peak`
（平均加 peak）或 `plain`（只有平均）。

## 注意事項

- **時間窗**接受 rrdtool 的寫法：`-1h`、`-90min`、`-7d`、`-2w`、`now-1d`，
  也吃 Unix 時間戳和 RFC3339。
- **取樣上限**為每次查詢 11000 點（Prometheus 的限制）。超過時會自動放寬
  `--step`，而不是讓查詢失敗。
- **時間軸依慣例**：時間戳 T 的樣本畫在「結束於 T」的格子裡，與 RRD/MRTG 相同。

## 目錄結構

```
tsgraph/               繪圖函式庫，可獨立使用
cmd/prometheus-render   CLI
internal/promapi        query_range 客戶端、時間解析、稠密化
internal/query          時間窗與 step 決策、平行抓取
internal/params         CLI 與 HTTP 共用的參數層
internal/render         把查詢結果接到函式庫
internal/server         /render 端點
examples/gallery        從 SQLite 產生 out/ 的範例程式（獨立 module）
testdata/sample.db      範例資料
hack/                   讀回渲染像素的檢查工具
```

## 授權

Apache 2.0，見 [`LICENSE`](LICENSE) 與 [`NOTICE`](NOTICE)。
