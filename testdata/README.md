# 範例資料

`sample.db` 是一個 SQLite 檔，裝著 `out/` 那些圖用到的時間序列。主機名稱已經改成
`core-1`、`edge-1` 這種通用名字，檔案裡沒有任何位址、標籤或可辨識的資訊。

```sql
CREATE TABLE series (
  id     INTEGER PRIMARY KEY,
  metric TEXT NOT NULL,   -- traffic | pps | rps
  host   TEXT NOT NULL,
  tier   TEXT NOT NULL,   -- 5m | 30m | 2h | 1d
  name   TEXT NOT NULL,   -- rx | tx | rx peak | tx peak | requests | peak
  ord    INTEGER NOT NULL,-- 繪製與圖例順序
  start  INTEGER NOT NULL,-- 第一個樣本的 unix 秒
  step   INTEGER NOT NULL -- 樣本間隔秒數
);

CREATE TABLE points (
  series_id INTEGER NOT NULL REFERENCES series(id),
  idx       INTEGER NOT NULL,
  value     REAL,          -- NULL 表示缺值
  PRIMARY KEY (series_id, idx)
) WITHOUT ROWID;
```

`value` 用 NULL 而不是 0 表示缺值，這樣「沒有資料」和「值是零」不會混為一談——
繪圖時前者是斷線，後者是貼著底線的一點。

看一下裡面有什麼：

```sh
sqlite3 testdata/sample.db \
  'SELECT metric, host, tier, COUNT(*) FROM series GROUP BY metric, host, tier'
```
