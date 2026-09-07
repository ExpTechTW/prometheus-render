# prometheus-render

從 **Prometheus** 或 **VictoriaMetrics** 直接畫出 RRDtool / MRTG / Munin 風格的圖。

English documentation: [README-EN.md](README-EN.md)

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
| `#00CC00` | 綠 | RX，填充 |
| `#0000FF` | 藍 | TX，線條 |
| `#006600` | 墨綠 | RX 的 peak |
| `#FF00FF` | 洋紅 | TX 的 peak |

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
prometheus-render --config site.yml --serve :8080
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

經典 MRTG 流量圖——RX 填充、TX 線條：

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

### 定時繪圖與 HTML 網頁

`--config` 吃一份 yml，把裡面每張圖畫成 MRTG 的四種時間尺度，產生頁面，然後照設定
的週期重畫：

```bash
prometheus-render --config site.yml
```

```yaml
source:
  url: http://localhost:9090

output:
  dir: site
  title: Network
  listen: ":8080"   # 留空則只寫檔，交給 nginx
  interval: 5m      # 0 表示畫一次就結束，給 cron 用
  workers: 0        # 0 = 每顆 CPU 一個

defaults:
  theme: mrtg
  dark_theme: dark  # 頁面上可切換，兩種主題共用同一次查詢
  peak: true        # MRTG 的峰值線，頁面上用按鈕切換
  area: first
  tz: Asia/Taipei
  # 省略 ranges 就是 MRTG 的四層：1d / 1w / 1m / 1y

graphs:
  - name: traffic
    title: Traffic
    vtitle: Mbps
    series:
      - {expr: 'sum(rate(nginx_http_in_bytes_total[5m])) * 8 / 1e6', legend: RX}
      - {expr: 'sum(rate(nginx_http_bytes_total[5m])) * 8 / 1e6',    legend: TX}
```

完整範例見 [`site.example.yml`](site.example.yml)。

#### 多地點

加上 `regions` 區塊，同一批圖就能兩種方式讀：**看一個地點的所有圖表**，或**看一項
圖表在所有地點的樣子**。

```yaml
regions:
  label: region              # 依這個標籤切分，也決定查詢裡的佔位符 $region
  match: '{job="nginx"}'     # 只從這些序列裡找值
  titles: {tnn: core-tnn1, tyo: core-tyo1}   # 名稱同時用在頁面、網址與圖上

graphs:
  - name: traffic
    series:
      - {expr: 'sum(rate(nginx_http_in_bytes_total{region="$region"}[5m]))', legend: RX}

  - name: sensor
    only_regions: [tnn]      # 只存在於一個地點的東西

  - name: total
    global: true             # 不切分，一張圖涵蓋全部
```

`titles` 給的名稱是站台的唯一稱呼——頁面、網址、圖上的標題都用它；原始標籤值只留在
查詢裡。因為會變成路徑片段，名稱限定為英數字與 `.-_`，不合的在載入時就報錯，而不是
悄悄在網址裡變成別的東西。

重畫的時刻對齊時鐘：週期 5 分鐘就畫在 :00、:05、:10，而不是從行程啟動的那一刻起算。
頁面用同一個間隔算出同樣的邊界，所以兩邊不必互相溝通——**沒有版本檔、沒有輪詢**。
頁面只拿到一個數字：間隔。

每輪會**提前 5 秒開始**畫，邊界到達時檔案就已經就位。某一輪如果畫得比提前量還久，
log 會警告。

頁首倒數到下一個邊界，時間到就換圖。**圖片網址是固定的**（沒有版本參數），所以前面
每一層快取照常命中。

頁面的 inline script 都帶 `data-cfasync="false"`，讓 Cloudflare 的 Rocket Loader
不要接管——它會把 `type` 改成瀏覽器不執行的值再自己非同步跑，結果就是倒數、切換、
主題全部失效。

重建 `<img>` 元素或 fetch 都無法單獨解決：瀏覽器在 HTTP 之上另有一層**圖片快取**，
以網址為鍵，而且刻意不遵守過期語意（HTML 規範稱之為 list of available images，
為相容性而存在）。指向已持有網址的 `<img>` 會直接從那裡算繪、完全不發請求；而 fetch
填的是 HTTP 快取，兩者是不同的儲存。

所以把抓到的位元組**直接交給元素**：`fetch` → `blob` → `createObjectURL`。這繞過了
以網址為鍵的那一層，而**線上的網址完全沒變**，nginx 與 CDN 的快取鍵和命中率不受影響。

**每一張圖都走這條路，包括第一張**——HTML 裡的 `<img>` 不帶 `src`。所以剛打開的頁面
不可能顯示到快取裡的舊圖。

每張圖右下角帶有重畫時間，同樣遵循 `defaults.tz`——圖被存下來或轉傳出去之後，
自己還說得出有多舊。

**值是從資料裡發現的**，所以增減節點不必改設定檔。某個地點查不到資料的圖不會出現在
頁面上；查詢失敗則保留，因為那代表「不知道」而不是「沒有」。

會重塑查詢的標籤值（含引號、反斜線、大括號的）會被拒絕而不是跳脫——真實的基礎設施
標籤不會長那樣。

#### 主題與峰值

每張圖都畫成 light 與 dark 兩種配色；`peak: true` 的圖另外畫出帶峰值線的版本。
頁面用按鈕切換，主題選擇記在 `localStorage`，峰值選擇記在每張圖上。

峰值是 MRTG 的作法：每個取樣桶取最大值而非平均，畫在平均線後面，所以只在高過平均
處露出。**每一層的峰值取自下一層較細的解析度**（年圖取自月圖的），這既符合 MRTG，
也讓子查詢的點數不會爆掉。

一次查詢餵四張圖——配色與峰值改變的是畫法，不是讀取的資料。

### 服務產出的頁面

`--serve` 把畫好的頁面服務出來，覆寫設定檔裡的 `output.listen`。它需要 `--config`：

```bash
prometheus-render --config site.yml --serve :8080
```

**服務出去的就是設定檔畫出來的東西，沒有任何端點接受查詢參數。** 這是刻意的：
一個吃 PromQL 的 URL 參數，等於讓能連到頁面的人決定這個程式讀什麼、花多少
成本去讀——那是注入面，不是功能。要多一張圖就在 yml 裡多寫一個 `graphs` 條目。

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
tsgraph/fonts           內嵌的字體，Maple Mono 的 ASCII 子集
cmd/prometheus-render   CLI
internal/promapi        query_range 客戶端、時間解析、稠密化
internal/query          時間窗與 step 決策、平行抓取
internal/params         CLI 與設定檔共用的參數層
internal/render         把查詢結果接到函式庫
internal/config         yml 設定檔
internal/site           定時繪圖、工作池與 HTML 產生
examples/gallery        從 SQLite 產生 out/ 的範例程式（獨立 module）
testdata/sample.db      範例資料
hack/                   讀回渲染像素的檢查工具
```

## 授權

Apache 2.0，見 [`LICENSE`](LICENSE) 與 [`NOTICE`](NOTICE)。
內嵌的字體是 Maple Mono，走 SIL Open Font License 1.1，見
[`tsgraph/fonts/OFL.txt`](tsgraph/fonts/OFL.txt)。
