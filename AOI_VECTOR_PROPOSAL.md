# AOI 矢量驱动的选景与裁剪 —— 设计方案（提案）

> 状态：**提案（未实现）**。当前保持 bbox 方案；本文记录“让用户丢一条线/面，
> scraper 自动按面发现+覆盖+裁剪”的设计，供后续按指令实施。
> 关联：选景三模式（all / clearest-per-tile / cloud-free-cover）见 `CONFIG.md`。

## 1. 背景与动机

现状的 per-tile 模式（`clearest-per-tile` / `cloud-free-cover`）按 **bbox 矩形**
发现瓦片。对**细长 AOI**（如铁路走廊）极不经济：

- 蒙内铁路 50km 缓冲走廊：与 bbox 矩形相交 **29** 个 MGRS 瓦片，
  但真正与缓冲**面**相交的只有 **~14** 个。
- 多下近一倍数据；且最终还要在 scraper 之外用 GDAL 按面裁剪一遍。

目标：用户给一个 **AOI 矢量**（点/线/面）+ 可选缓冲距离，scraper 自动：
1. 缓冲成 AOI 面
2. 只发现与 AOI **面**真正相交的瓦片（14 而非 29）
3. 按 AOI∩瓦片 做 `cloud-free-cover` 覆盖
4. 下载后直接按 AOI 面裁剪输出

—— 把输入从“bbox + 猜瓦片”降为“丢一条线 + 缓冲距离”，更少输入、更智能。

## 2. 新增配置字段

| 字段 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `aoi` | string | - | AOI 矢量文件路径（GeoJSON/SHP/KML/GPKG）或内联 GeoJSON 字符串 |
| `buffer_km` | float | 0 | 对点/线 AOI 的缓冲距离（km）；面 AOI 可设 0 |
| `clip_to_aoi` | bool | true | 输出是否按 AOI 面做 cutline 裁剪 |

与 `bbox` 的关系：**提供 `aoi` 时，`bbox` 由 AOI 包络自动推导**（仅用于 STAC 检索的
粗筛）；用户不再需要手写 bbox。两者都给时以 `aoi` 为准、`bbox` 忽略。

## 3. 工作流

```
AOI 矢量 ──(可选 buffer_km)──► AOI 面(WGS84)
   │
   ├─► 包络 bbox ──► STAC 逐瓦片/发现检索（现有逻辑）
   │
   ├─► discoverTiles 后：用 瓦片足印 ∩ AOI面 过滤
   │       只保留相交面积 ≥ 阈值(如 30 km²) 的瓦片，剔除擦边碎片
   │
   ├─► cloud-free-cover：集合覆盖目标改为 AOI∩瓦片（而非 bbox∩瓦片）
   │
   └─► 下载 + RGBA 合成后：gdalwarp -cutline <AOI> 裁出走廊；可选整体镶嵌
```

## 4. 几何实现（零外部 Go 依赖约束）

scraper 坚持**纯 Go 标准库**。三条路径：

| 方案 | 做法 | 评价 |
|------|------|------|
| **A. 复用已有 ray-casting** | `cloud-free-cover` 已实现的点在多边形（`pointInRing`）可直接用于“瓦片足印中心/网格 ∩ AOI”判定与覆盖统计 | ✅ 纯 stdlib，无新依赖，推荐用于**发现/覆盖判定** |
| **B. 复用已捆绑的 GDAL/OGR** | scraper 已捆绑 GDAL（做 RGB 合成）。用 `ogr2ogr` 读取/转换/缓冲矢量，用 `gdalwarp -cutline` 裁剪 | ✅ 复用现有 bundle，**矢量格式齐全 + 精确裁剪**，推荐用于**读取与最终裁剪** |
| C. 引入 orb/go-geom | 第三方几何库 | ❌ 破坏零依赖原则，不采用 |

**推荐组合 = A + B**：
- **矢量读取/缓冲**：交给已捆绑 GDAL —— `ogr2ogr -f GeoJSON -t_srs EPSG:4326`
  把任意输入统一成 WGS84 GeoJSON，再用 stdlib `encoding/json` 解析外环坐标。
  缓冲可用 `ogr2ogr -dialect SQLITE -sql "SELECT ST_Buffer(geometry, <deg>) ..."`
  或在合适 UTM 带做米制缓冲（注意度/米换算）。
- **瓦片发现/覆盖**：用已有 `pointInRing`，把瓦片足印对 AOI 外环做相交判定与
  网格集合覆盖（与 `greedyCoverScenes` 同一套机制）。
- **最终裁剪**：`gdalwarp -cutline <aoi.shp/geojson> -crop_to_cutline`。

## 5. 与现有功能的协同

- `select_mode` 仍三选一；AOI 只改变“目标区域”：bbox 矩形 → AOI 面。
- `cloud-free-cover` 的集合覆盖 target 从 `bbox∩瓦片` 改为 `AOI∩瓦片`，
  自然只覆盖走廊、不浪费角落。
- `-list-tiles`：AOI 模式下打印**与 AOI 面相交**的瓦片（比 bbox 更准、更少）。
- `max_nodata` / `coverage_target` / `max_per_tile` 语义不变。

## 6. 配置示例

```json
{
  "satellite": "sentinel-2",
  "aoi": "mengnei_railway_sgr.geojson",
  "buffer_km": 50,
  "start_date": "2025-01-01",
  "end_date": "2026-06-27",
  "max_cloud": 5,
  "select_mode": "cloud-free-cover",
  "clip_to_aoi": true,
  "bands": ["red", "green", "blue"]
}
```
用户只给：一条铁路线矢量 + 缓冲 50km + 云量 5% + 选 ③ 模式。其余全自动。

## 7. 实施步骤（增量、低风险）

1. **配置**：加 `aoi` / `buffer_km` / `clip_to_aoi` 字段 + 校验（文件存在/可解析）。
2. **矢量读取助手**：`ogr2ogr → GeoJSON(WGS84) → 解析外环`；实现 `buffer_km`。
3. **AOI 包络 → bbox**：推导 bbox 注入现有检索路径。
4. **瓦片过滤**：`discoverTiles` 结果用 `pointInRing`(瓦片 vs AOI) + 面积阈值过滤。
5. **覆盖目标**：`cloud-free-cover` 的网格 target 限定在 AOI∩瓦片。
6. **输出裁剪**：下载/合成后 `gdalwarp -cutline <AOI>`；（可选）整体镶嵌。
7. **测试**：合成 AOI 多边形、点在多边形、瓦片过滤、端到端（小 AOI）。

## 8. 开放问题 / 取舍

- **矢量格式**：全部经 GDAL（格式全、稳）vs 仅支持 GeoJSON（纯 stdlib、更轻）。
  建议经 GDAL（已捆绑，零额外成本）。
- **缓冲投影**：自动选 UTM 带 vs 固定等积投影（EPSG:6933）。跨带 AOI 需注意。
- **MultiPolygon / 带洞多边形**：`pointInRing` 需扩展为支持多环（外环 - 内环）
  和多面；`STACItem.Geometry` 目前只建模单环 Polygon，足印一般够用，AOI 侧要支持。
- **整体镶嵌**：当前由外部 Python 完成；是否在 scraper 内顺带产出走廊镶嵌图？
  （涉及跨 UTM 带统一投影 + VRT，工作量中等。）
- **`clip_to_aoi=false`**：仅按瓦片下载、不裁，便于用户自行后处理。

## 9. 预期收益

- 蒙内走廊：瓦片 **29 → 14**，下载量近半；输出直接是走廊面，免外部裁剪。
- 输入从“bbox + 手动瓦片”降为“一条线 + 缓冲距离”，契合“少输入、更智能”。

---

### 附：当前 bbox 方案的等效手工流程（在 AOI 功能落地前）

1. `sentinel-scraper -list-tiles` 看 bbox 内瓦片 → 人工挑走廊相关的填 `tiles`；
2. `select_mode: cloud-free-cover` 下载；
3. 外部 `gdalwarp -cutline <buffer.shp> -crop_to_cutline` 裁成走廊 + VRT 镶嵌。

AOI 方案即把第 1、3 步并入 scraper、自动化。
