# 参数配置说明

sentinel-scraper 的配置分为两个文件：

- **config.json** — 你要下载什么数据（卫星、产品、区域、时间等）
- **settings.json** — 认证信息（CDSE、Earthdata）

程序根据 `config.json` 中的卫星类型，自动选择最佳下载来源，无需手动指定 source。

---

## config.json

### S2 L2A

```json
{
  "satellite": "sentinel-2",
  "bbox": [116.2, 39.8, 116.6, 40],
  "start_date": "2026-05-01",
  "end_date": "2026-06-05",
  "min_cloud": 0,
  "max_cloud": 20,
  "bands": ["red", "green", "blue"],
  "limit": 20,
  "max_workers": 4,
  "max_retries": 3
}
```

### S1 GRD

```json
{
  "satellite": "s1",
  "product": "grd",
  "bbox": [116.2, 39.8, 116.6, 40],
  "start_date": "2026-05-01",
  "end_date": "2026-06-05",
  "bands": ["vv", "vh"],
  "limit": 5,
  "max_workers": 2
}
```

### S1 SLC

```json
{
  "satellite": "s1",
  "product": "slc",
  "bbox": [116.2, 39.8, 116.6, 40],
  "start_date": "2026-05-01",
  "end_date": "2026-06-05",
  "bands": ["vv", "vh"],
  "limit": 5,
  "max_workers": 2
}
```

### HLS

```json
{
  "satellite": "hls",
  "bbox": [116.2, 39.8, 116.6, 40],
  "start_date": "2026-05-01",
  "end_date": "2026-06-05",
  "bands": ["red", "green", "blue"],
  "limit": 10
}
```

### 字段说明

| 字段 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `satellite` | string | 否 | `sentinel-2` | `sentinel-1`/`s1`, `sentinel-2`/`s2`, `hls` |
| `product` | string | 否 | `grd` | `grd` 或 `slc`，仅 `s1` 时有效 |
| `bbox` | [float] | 是 | - | [west, south, east, north] |
| `start_date` | string | 是 | - | `YYYY-MM-DD` |
| `end_date` | string | 是 | - | `YYYY-MM-DD` |
| `min_cloud` | float | 否 | 0 | 最小云量（%），仅 S2/HLS |
| `max_cloud` | float | 否 | 0 | 最大云量（%），仅 S2/HLS |
| `bands` | [string] | 否 | 卫星默认 | 下载波段 |
| `limit` | int | 否 | 20 | 最大结果数 |
| `max_workers` | int | 否 | 4 | 并发下载数 |
| `max_retries` | int | 否 | 3 | 下载重试次数 |

---

## settings.json

只存认证，不存来源、卫星、collection 等。

```json
{
  "cdse_auth": {
    "username": "your@email.com",
    "password": "your_cdse_password"
  },
  "earthdata_auth": {
    "username": "your_earthdata_username",
    "password": "your_earthdata_password"
  }
}
```

配置方式：运行 `sentinel-scraper.exe -setup-auth` 交互式填写，或 `-setup` 打开 Web 页面填写。

---

## 自动编排

程序根据卫星类型和可用认证自动选择来源，一个失败自动切换下一个：

| 卫星 | 有 CDSE 认证 | 有 Earthdata 认证 | 编排顺序 |
|------|-------------|------------------|----------|
| **S2** | 是 | - | Earth Search → CDSE STAC → CDSE OData |
| **S2** | 否 | - | Earth Search |
| **S1 GRD/SLC** | - | 是 | Earth Search → ASF |
| **S1 GRD/SLC** | - | 否 | Earth Search |
| **HLS** | - | 是 | Earthdata |
| **HLS** | - | 否 | 报错（需 Earthdata 认证） |

---

## 向后兼容

旧版 `collection` 字段（如 `sentinel-2-l2a`、`sentinel-1-grd`）仍然有效，程序会自动解析。
