# sentinel-scraper 重构方案

把单文件 `main.go`（1707 行）按职责拆分成多个源文件，所有文件保持在 `package main`，纯文件重组、不改函数名/签名。

## 一、文件拆分总览

| 文件 | 职责 | 估算行数 |
|------|------|---------|
| `main.go` | 入口、标志、配置加载、auth 构建、调度、worker 池、STAC 流程编排 | ~220 |
| `config.go` | 任务级配置（`Config`/`SearchOptions`/`AuthConfig`）+ 加载逻辑 | ~80 |
| `settings.go` | 用户级设置持久化 + CLI 向导 + Web 向导 | ~340 |
| `auth.go` | `Authenticator` 接口、`NoOpAuth`、`CDSEAuth`（Keycloak token） | ~90 |
| `download.go` | 共享传输基础设施：进度条、字节格式化、`Content-Range` 解析 | ~50 |
| `stac.go` | STAC 模式（Earth Search + CDSE STAC + 自定义合并） | ~600 |
| `odata.go` | OData 模式（CDSE 整景 ZIP） | ~340 |
| `gdal.go` | GDAL 工具发现 + RGB 合成 | ~95 |
| `main_test.go` | 不动 | ~390 |

**Earth Search 与 STAC 合并的理由**：两者协议完全相同，差异（URL/认证/波段命名）已经全部参数化（`Config.STACURL`、`Authenticator`、`resolveAssetKey()` 按 URL 自动判断）。再分两个文件只会复制粘贴。

## 二、各文件具体内容

### main.go (~220 行)

**全局**：
- `EarthSearchURL`、`Collection`、`DownloadTimeout`

**`main()` 流程**：
1. 标志解析（`-config`/`-dest`/`-setup-auth`/`-setup`）
2. 调用 `setupAuthWizard()` 或 `runSetupWizard()`（如需要）
3. `LoadConfig()` + `mergeSettings()`
4. 构造 `Authenticator`（`NoOpAuth` 或 `NewCDSEAuth`）
5. 打印运行参数摘要
6. **按 `Settings.Source` 分支**：
   - `cdse_odata` → 调用 `runODataFlow(cfg, auth, dest)` 后返回
   - 其他 → 进入 STAC 流程（下面）
7. **STAC 流程编排**（保留在 main 里）：
   - `SearchItems()` → `FilterItemsByCloud()` → `PrintItemSummary()`
   - 扫描 `destDir` 已有文件，对缺 KML 的调用 `fetchItem()` + `SaveKML()` 补登
   - **创建 `tasks`/`results` channel + 启动 worker 池**（用户要求保留在 main）
   - 遍历 items：调用 `SaveKML(item)`，按波段构造 `downloadTask` 推入 channel
   - 关闭 tasks，drain results，统计 failed/skipped
   - 遍历 items 调用 `BuildRGB()`
8. 最终汇总打印

### config.go (~80 行)

- 类型：`Config`、`SearchOptions`、`AuthConfig`
- 函数：`resolveEnv()`、`LoadConfig()`、`mergeSettings()`

> `mergeSettings` 操作的是 `Config`，逻辑上和 `LoadConfig` 是一组；放 `config.go` 比放 `settings.go` 更顺。

### settings.go (~340 行)

- 类型：`Settings`
- 持久化：`settingsPath()`、`loadSettings()`、`saveSettings()`、`needsSetup()`
- CLI 向导：`setupAuthWizard()`、`readLine()`、`stdinReader`
- Web 向导：`runSetupWizard()`、`openBrowser()`、`setupHTML`、`successHTML`

### auth.go (~90 行)

- 接口：`Authenticator`
- 实现：`NoOpAuth`、`CDSEAuth`、`NewCDSEAuth()`、`(o *CDSEAuth) Apply/tokenWithRefresh/fetchToken()`

### download.go (~50 行)

只放真正跨模式共享的传输工具：

- `progressReader` 类型 + `Read()` 方法
- `formatBytes()`
- `parseContentRangeTotal()`

> `downloadTask`/`downloadResult`/`downloadWorker`/`DownloadAsset` 都是 STAC 专属（写 `<id>_<band>.tif`），放 `stac.go` 更内聚。

### stac.go (~600 行)

**类型**：
- `STACItemCollection`、`Geometry`、`STACItem`、`STACProperties`、`AlternateLink`、`Asset`
- `downloadTask`、`downloadResult`

**波段映射**：
- `cdseBandMap`、`resolveAssetKey()`

**搜索/查询**：
- `SearchItems()`、`FilterItemsByCloud()`、`PrintItemSummary()`、`fetchItem()`

**资产/文件辅助**：
- `knownBands`、`parseItemIDFromFilename()`、`resolveDownloadURL()`、`scanExistingItems()`、`assetExists()`

**KML**：
- `SaveKML()`（STAC 用）

**下载**：
- `DownloadAsset()`、`downloadWorker()`

### odata.go (~340 行)

**常量**：
- `cdseODataCatalogURL`、`cdseODataDownloadURL`

**类型**：
- `odataProduct`、`odataCatalogResponse`

**搜索**：
- `queryODataProducts()`

**KML**：
- `SaveKMLForOData()`

**下载**：
- `downloadODataProductOnce()`、`odataWriteBody()`、`downloadODataProduct()`

**流程**：
- `runODataFlow()`

### gdal.go (~95 行)

- `findGDALTool()`、`gdalEnv()`、`BuildRGB()`

## 三、实施约束

1. **不改函数名、不改签名** — 纯文件重组，降低风险，`main_test.go` 零修改
2. **同一 package `main`** — Go 多文件同包互相可见，无需 export 任何东西
3. **`Geometry` 类型现在被 STAC 和 OData 都引用** — 放 `stac.go`，`odata.go` 直接引用同包类型即可（无循环依赖问题）
4. **`progressReader`/`formatBytes`/`parseContentRangeTotal`** — STAC 和 OData 都用，统一放 `download.go`

## 四、验证步骤

迁移完成后依次执行：

```bash
go build ./...
go vet ./...
go fmt ./...
go test ./...
```

## 五、后续可优化项（本次不做）

下面这些是分文件后才能看清的重复，本次重构只搬不改，留作后续工单：

- `SaveKML` vs `SaveKMLForOData`：可抽出共享 KML 模板构造器
- `DownloadAsset`（STAC）和 `downloadODataProductOnce/odataWriteBody`（OData）：HTTP `Range`/`StatusPartialContent`/续传分支结构相似，可抽 `resumableDownload()` 工具
- `main.go` 里的 STAC 流程编排，未来想把 worker 池抽到 `download.go` 时也能整体迁出
