# Sentinel-2 Go 下载器

[English](README.en.md) | 中文

一个轻量级的 Go 命令行工具，用于查询并下载 Sentinel-2 L2A 卫星影像。支持多种数据源、网页与终端配置向导、断点续传、自动 RGB 合成与去黑边裁切，输出为 Cloud Optimized GeoTIFF（COG）。**纯 Go 标准库实现，零外部 Go 依赖。**

> 当前版本：**v1.0**

## 特性

- **三种数据源 + Sentinel-1 SAR 支持**
  - Earth Search STAC（AWS 公开，无需认证）
  - CDSE STAC（哥白尼数据空间，按波段 COG）
  - CDSE OData（哥白尼数据空间，整景 SAFE ZIP）
  - Sentinel-1 GRD / SLC（SAR 雷达数据，VV/VH 极化，不受云影响）
- **配置向导**：首次运行自动打开浏览器，也支持终端 SSH 模式
- **友好波段名**：`red` / `green` / `blue` / `nir` 等，自动映射到各数据源对应的资源键
- **断点续传**：基于 HTTP `Range`，已下载文件自动跳过
- **并发下载**：可配置 worker 池
- **RGB / RGBA 合成**：通过 GDAL 自动生成 8-bit RGB 合成图，并自动**去黑边**裁出真实成像范围
- **KML 输出**：每个影像生成独立 KML 文件，方便在 Google Earth 等软件中预览覆盖范围
- **认证**：CDSE Keycloak OAuth2 密码模式，自动刷新 token

## 快速开始

```bash
git clone <你的仓库地址>
cd sentinel2-go
go build -o sentinel2-scraper .

# 首次运行 — 自动打开浏览器进行配置
./sentinel2-scraper
```

首次运行时，如果 `~/.sentinel2-go/settings.json` 不存在，程序会自动启动本地 HTTP 服务并打开浏览器，引导你选择数据源和填写认证信息。配置完成后程序自动继续。

## 配置向导

### 首次运行（自动）

```bash
./sentinel2-scraper
```

### 手动重新配置

```bash
# 网页向导（打开浏览器）
./sentinel2-scraper -setup

# 终端向导（无图形界面 / SSH 友好）
./sentinel2-scraper -setup-auth
```

### 数据源选项

| 选项 | 说明 | 认证 |
|------|------|------|
| **Earth Search STAC API** | AWS 托管的公开 STAC，按波段下载 | 无 |
| **CDSE STAC API** | 哥白尼数据空间，按波段 COG 下载 | 用户名 + 密码 |
| **CDSE OData API** | 哥白尼数据空间，整景 SAFE ZIP 下载 | 用户名 + 密码 |
| **自定义 STAC** | 任何兼容的 STAC API 端点 | 无 |

### CDSE 注册步骤

1. 访问 [dataspace.copernicus.eu](https://dataspace.copernicus.eu/) 注册账号
2. 查收验证邮件并完成验证
3. 在配置向导中填写 CDSE 登录邮箱与密码
4. 保存并继续

配置文件保存在 `~/.sentinel2-go/settings.json`，权限 `0600`（仅所有者可读写）。**密码以明文存储**，请妥善保护用户目录权限。

### 数据源对比

| 维度 | Earth Search STAC | CDSE STAC | CDSE OData | Sentinel-1 |
|------|-------------------|-----------|------------|------------|
| **下载粒度** | 单波段 COG（50–200 MB / 波段） | 单波段 COG（50–200 MB / 波段） | 整景 ZIP（500 MB–1 GB+） | 单波段 COG / 整景 ZIP |
| **认证** | 无 | 需 CDSE 账号 | 需 CDSE 账号 | 无 / 需 CDSE 账号 |
| **速度** | 快（AWS CloudFront CDN） | 中等（欧盟直链） | 慢（实时打包 + 大文件） | 中等 |
| **国内访问** | 多数情况下需翻墙 | 多数情况下需翻墙 | 大概率免翻墙 | 大概率免翻墙（OData） |
| **断点续传** | ✅ | ✅ | ✅ | ✅ |
| **RGB 合成** | ✅ 自动 | ✅ 自动 | ✅ 自动（解压 R10m B02/B03/B04 后合成） | ❌ SAR 不做 RGB |
| **云量过滤** | ✅ | ✅ | ✅ | ❌ SAR 不受云影响 |
| **KML 输出** | ✅ | ✅ | ✅ | ✅ |

**选型建议：**

- **网络好，追求速度** → Earth Search STAC（默认，最快）
- **Earth Search 连不上，或需要官方源** → CDSE STAC（按波段）
- **需要完整 SAFE 产品（含全部波段 + 元数据）或国内免翻墙** → CDSE OData
- **需要 SAR 雷达数据（全天候、穿透云层）** → Sentinel-1 GRD/SLC

### `settings.json` 示例

```json
{
  "source": "cdse",
  "stac_url": "https://stac.dataspace.copernicus.eu/v1",
  "collection": "sentinel-2-l2a",
  "auth": {
    "username": "your-email@example.com",
    "password": "your-password"
  }
}
```

`source` 可取 `earth_search`（或留空）/ `cdse` / `cdse_odata` / `custom`。

## 配置说明

### `config.json` — 查询参数

```json
{
  "bbox": [116.2, 39.8, 116.6, 40.0],
  "start_date": "2026-04-01",
  "end_date": "2026-04-15",
  "min_cloud": 0,
  "max_cloud": 20.0,
  "bands": ["red", "green", "blue", "nir"],
  "satellite": "sentinel-2-l2a",
  "limit": 20,
  "max_workers": 4,
  "max_retries": 3
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| 字段 | 类型 | 说明 |
|------|------|------|
| `bbox` | `[float64]` | 边界框 `[西, 南, 东, 北]` |
| `start_date` | `string` | 起始日期 `YYYY-MM-DD` |
| `end_date` | `string` | 结束日期 `YYYY-MM-DD` |
| `min_cloud` | `float64` | 最小云量百分比（0–100，仅 Sentinel-2） |
| `max_cloud` | `float64` | 最大云量百分比（0–100，仅 Sentinel-2） |
| `bands` | `[string]` | 待下载的波段/极化列表（友好名称） |
| `satellite` | `string` | 卫星类型：`sentinel-2-l2a` / `sentinel-1-grd` / `sentinel-1-slc` |
| `limit` | `int` | STAC 查询返回上限（默认 20） |
| `max_workers` | `int` | 并发下载线程数（默认 4） |
| `max_retries` | `int` | 单文件失败重试次数（默认 3） |

### 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-config` | `config.json` | 查询配置 JSON 路径 |
| `-dest` | `./sentinel2_data` | 下载文件保存目录 |
| `-setup` | — | 打开网页配置向导 |
| `-setup-auth` | — | 打开终端配置向导 |

### 环境变量

`config.json` 中可使用 `${VAR}` 引用环境变量：

```json
{
  "auth": {
    "username": "${CDSE_USERNAME}",
    "password": "${CDSE_PASSWORD}"
  }
}
```

## 波段映射

在 `config.json` 中使用 **友好名称**，程序根据数据源自动映射到对应的资源键。

### Earth Search 波段

| 友好名称 | Earth Search 键 | Sentinel-2 波段 |
|----------|----------------|-----------------|
| `coastal` | `coastal` | B01 |
| `blue` | `blue` | B02 |
| `green` | `green` | B03 |
| `red` | `red` | B04 |
| `rededge1` | `rededge1` | B05 |
| `rededge2` | `rededge2` | B06 |
| `rededge3` | `rededge3` | B07 |
| `nir` | `nir` | B08 |
| `nir08` | `nir08` | B8A |
| `nir09` | `nir09` | B09 |
| `swir16` | `swir16` | B11 |
| `swir22` | `swir22` | B12 |
| `scl` | `scl` | SCL |

### CDSE 波段（自动映射）

| 友好名称 | CDSE Asset 键 | 分辨率 |
|----------|---------------|--------|
| `coastal` | `B01_60m` | 60m |
| `blue` | `B02_10m` | 10m |
| `green` | `B03_10m` | 10m |
| `red` | `B04_10m` | 10m |
| `rededge1` | `B05_20m` | 20m |
| `rededge2` | `B06_20m` | 20m |
| `rededge3` | `B07_20m` | 20m |
| `nir` | `B08_10m` | 10m |
| `nir08` | `B8A_20m` | 20m |
| `nir09` | `B09_60m` | 60m |
| `swir16` | `B11_20m` | 20m |
| `swir22` | `B12_20m` | 20m |
| `scl` | `SCL_20m` | 20m |
| `aot` | `AOT_20m` | 20m |
| `wvp` | `WVP_10m` | 10m |
| `tci` | `TCI_10m` | 10m |

> 例如：`"bands": ["red", "green", "blue"]` 在 CDSE STAC 模式下会自动下载 `B04_10m` / `B03_10m` / `B02_10m`，但保存为 `<item>_red.tif` / `<item>_green.tif` / `<item>_blue.tif`，与 RGB 合成流程兼容。

### Sentinel-1 极化方式

| 友好名称 | STAC Asset 键 | 说明 |
|----------|---------------|------|
| `vv` | `vv` | 垂直发射 / 垂直接收 |
| `vh` | `vh` | 垂直发射 / 水平接收 |
| `hh` | `hh` | 水平发射 / 水平接收 |
| `hv` | `hv` | 水平发射 / 垂直接收 |

> Sentinel-1 数据不受云影响，不支持云量过滤和 RGB 合成。

## 输出

### STAC 模式 — Sentinel-2（Earth Search / CDSE STAC）

```
sentinel2_data/
  S2A_50TMK_20250105_0_L2A_red.tif
  S2A_50TMK_20250105_0_L2A_green.tif
  S2A_50TMK_20250105_0_L2A_blue.tif
  S2A_50TMK_20250105_0_L2A_nir.tif
  S2A_50TMK_20250105_0_L2A_rgba.tif    ← RGB + Alpha 去黑边合成图
  S2A_50TMK_20250105_0_L2A.kml         ← 影像范围 KML
  ...
```

CDSE STAC 的源文件为 JPEG 2000（`.jp2`），GDAL 工具可直接读取。RGB 输出统一拉伸为 8-bit GeoTIFF（固定 0–3000 → 0–255），并通过 `gdal_trace_outline` + `gdalwarp` + `gdal_merge_simple` 生成带 Alpha 通道的 RGBA 影像，自动剔除影像四周的 nodata 黑边。

### STAC 模式 — Sentinel-1（Earth Search / CDSE STAC）

```
sentinel2_data/
  S1A_IW_GRDH_1SDV_20250105_030000_039A_vv.tif
  S1A_IW_GRDH_1SDV_20250105_030000_039A_vh.tif
  S1A_IW_GRDH_1SDV_20250105_030000_039A.kml
  ...
```

Sentinel-1 SAR 数据直接下载为原始极化文件，**不做 RGB 合成**。

### OData 模式（CDSE OData）

```
sentinel2_data/
  S2A_T50TMK_20250105T030529_MSIL2A.zip            ← 完整 SAFE 产品
  S2A_T50TMK_20250105T030529_MSIL2A_rgba.tif       ← 自动 RGB 去黑边
  S2A_T50TMK_20250105T030529_MSIL2A.kml            ← 范围 KML
  ...
```

OData 模式同样会自动从 ZIP 中提取 `R10m/B02/B03/B04` 三个 JP2 进行 RGB 合成，并执行去黑边流程。完整产品 ZIP 始终保留，可用 SNAP / ENVI 等软件做后续处理。

## 编译与运行

```bash
# 直接构建
go build -o sentinel2-scraper .

# 或用 Makefile
make build           # 等价于 go build -o sentinel2-scraper .
make run             # 构建并运行
make fmt             # go fmt ./...
make vet             # go vet ./...
make clean           # 清理产物
make package         # Windows + GDAL 工具一并打包
make docker          # 构建 Docker 镜像
```

测试：

```bash
go test ./...
```

## Docker

```bash
docker build -t sentinel2-scraper .
docker run --rm \
  -v $(pwd)/config.json:/app/config.json \
  -v $(pwd)/sentinel2_data:/app/sentinel2_data \
  sentinel2-scraper
```

## 项目结构

代码位于 `package main`，按职责拆分为 9 个 Go 文件：

| 文件 | 职责 |
|------|------|
| `main.go` | CLI 入口、STAC 流程编排、worker 池 |
| `config.go` | `Config` / `SearchOptions` 结构、配置加载与合并 |
| `settings.go` | 用户级持久化设置、网页 / CLI 配置向导 |
| `auth.go` | 认证抽象、CDSE Keycloak OAuth2 密码模式 |
| `stac.go` | STAC 搜索、云量过滤、按波段下载 |
| `odata.go` | CDSE OData 搜索、整景 ZIP 下载、JP2 提取与 RGB 合成 |
| `gdal.go` | GDAL 工具发现、`BuildRGB` / RGBA 去黑边 |
| `kml.go` | 共享的 KML 写入辅助 |
| `download.go` | 共享的 `Range` 断点续传与进度显示 |

## 常见问题

**Q：选哪种数据源？**

- **首选 Earth Search**：速度最快，AWS CloudFront 全球加速；但部分网络可能不通
- **Earth Search 不可达** → 切到 **CDSE STAC**：按波段下载，文件较小
- **国内免翻墙 / 需要完整 SAFE 产品** → **CDSE OData**：整景 ZIP，慢但完整
- **需要 SAR 雷达数据（全天候、穿透云层）** → **Sentinel-1**：选择 GRD 或 SLC

**Q：Sentinel-1 和 Sentinel-2 有什么区别？**

- **Sentinel-2**：多光谱光学影像，受云影响，适合做植被监测、土地利用等
- **Sentinel-1**：C 波段 SAR 雷达，**不受云影响**，适合做地表形变监测、洪水监测、海上目标检测等
- Sentinel-1 不需要云量过滤，也不做 RGB 合成

**Q：如何下载 Sentinel-1 数据？**

在 `config.json` 中设置 `"satellite": "sentinel-1-grd"`（或 `"sentinel-1-slc"`），`"bands": ["vv", "vh"]`：

```json
{
  "satellite": "sentinel-1-grd",
  "bands": ["vv", "vh"],
  "bbox": [116.2, 39.8, 116.6, 40.0],
  "start_date": "2026-04-01",
  "end_date": "2026-04-15"
}
```

**Q：下载失败 / 超时怎么办？**

- Earth Search / CDSE STAC：每个文件约 50–200 MB，单文件超时 10 分钟
- CDSE OData：整景 ZIP 通常 500 MB–1 GB+，单文件超时 30 分钟
- 网络不稳定时，调高 `config.json` 中的 `max_retries`（如 3 或 5）
- 中断后重新运行会自动断点续传

**Q：搜索没有返回数据？**

- 确认日期范围在 Sentinel-2 归档时间内
- 确认 bbox 在陆地范围内
- 调高 `max_cloud` 或暂时去掉云量限制
- 不同数据源的覆盖时间略有差异

**Q：如何切换数据源？**

```bash
./sentinel2-scraper -setup
```

随时可以切换，已下载的文件不受影响。

**Q：能否使用自定义 STAC API？**

可以。在配置向导中选择"自定义 STAC API"，填写端点 URL 和 Collection 名称。

## 许可证

MIT
