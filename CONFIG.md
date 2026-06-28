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
| `max_cloud` | float | 否 | 0 | 最大云量（%），仅 S2/HLS；0 = 不过滤 |
| `max_nodata` | float | 否 | 100 | 最大空值像元百分比（0–100），仅 S2；用于剔除“部分条带”景，**100 = 不过滤**（未设即默认 100） |
| `select_mode` | string | 否 | `all` | 选景策略，仅 S2 的 per-tile 模式：`all` / `clearest-per-tile` / `cloud-free-cover`（见下节） |
| `tiles` | [string] | 否 | 自动发现 | 显式 MGRS 瓦片码列表（**不带** `MGRS-` 前缀，如 `["37MDS","37MDR"]`），仅 per-tile 模式有效；留空则从 bbox 自动发现 |
| `coverage_target` | float | 否 | 0.995 | `cloud-free-cover` 每瓦片目标覆盖率（0–1） |
| `max_per_tile` | int | 否 | 6 | `cloud-free-cover` 每瓦片最多叠加景数 |
| `bands` | [string] | 否 | 卫星默认 | 下载波段 |
| `limit` | int | 否 | 20 | 最大结果数 |
| `max_workers` | int | 否 | 4 | 并发下载数 |
| `max_retries` | int | 否 | 3 | 下载重试次数 |

---

## S2 智能选景（少云 · 满覆盖 · 无缝镶嵌）

`select_mode` 控制如何从检索结果里挑选场景，**仅 Sentinel-2 的 per-tile 模式可用**。
配合 `max_nodata` 覆盖率过滤，可一步得到“少云、无空洞”的镶嵌底图，用户只需给
bbox，无需手动指定瓦片。

| 模式 | 行为 | 适用 |
|------|------|------|
| `all`（默认） | 返回符合 `max_cloud`/`max_nodata` 过滤的**全部**场景 | 时间序列、变化检测、样本采集、存档 |
| `clearest-per-tile` | 每个 MGRS 瓦片只取**最清晰的一景**（满覆盖优先，云量最低） | 快速单期底图 |
| `cloud-free-cover` | 每个瓦片用**最少的若干景**（贪心集合覆盖）拼满，自动叠加互补条带填洞 | 无缝少云镶嵌产品 |

> `clearest-per-tile` / `cloud-free-cover` 仅支持 `satellite=sentinel-2`；
> 瓦片为空时自动从 bbox 发现（已分页，完整枚举）。

### 工作原理（为什么需要这两个模式）

Sentinel-2 数据按 110×110 km 的 MGRS 瓦片组织，但卫星刈幅切过瓦片边缘时，单景
可能只覆盖瓦片一角（“部分条带”），而其 `eo:cloud_cover` 只统计有数据的部分——
于是一个“0% 云”的景可能 80% 是空洞。

- `max_nodata`（如 5）：用 `s2:nodata_pixel_percentage` 过滤掉这类部分条带，确保单景满覆盖。
- `clearest-per-tile`：逐瓦片各取最优，避免“一刀切日期”被最差瓦片拖累。
- `cloud-free-cover`：当某瓦片**没有任何单景**满覆盖时，自动叠加 2~N 景互补足印拼满。

### 示例：无缝 50km 走廊底图（最小输入）

```json
{
  "satellite": "sentinel-2",
  "bbox": [36.44, -4.51, 40.10, -0.90],
  "start_date": "2025-01-01",
  "end_date": "2026-06-27",
  "max_cloud": 5,
  "select_mode": "cloud-free-cover",
  "coverage_target": 0.995,
  "max_per_tile": 6,
  "bands": ["red", "green", "blue"]
}
```
`tiles` 留空 → 自动发现相交瓦片；每瓦片用最少景拼到 ≥99.5% 覆盖，全部 < 5% 云。

### 示例：每瓦片一景的快速底图

```json
{
  "satellite": "sentinel-2",
  "bbox": [116.2, 39.8, 116.6, 40.0],
  "start_date": "2025-01-01", "end_date": "2025-03-31",
  "max_cloud": 5,
  "max_nodata": 5,
  "select_mode": "clearest-per-tile",
  "bands": ["red", "green", "blue"]
}
```

### 预览瓦片清单（不下载）

```bash
sentinel-scraper -list-tiles -config config.json
```
按 bbox 自动发现并打印所有相交的 MGRS 瓦片（已分页，完整枚举），随后退出、不下载。
可把输出粘进 `tiles` 字段固定瓦片集，或直接留空让运行时自动发现。

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
