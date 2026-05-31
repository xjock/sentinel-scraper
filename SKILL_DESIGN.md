# sentinel-scraper Skill 化设计方案

> 目标：将单机 CLI 工具升级为可供其他 agent / 服务调用的可编排能力。

---

## 背景

当前 `sentinel-scraper` 是一个单文件 Go CLI，功能聚焦：
1. 查询 Earth Search STAC API
2. 按云量、日期、BBox 过滤 Sentinel-2 L2A 影像
3. 并发下载指定波段（COG）
4. 构建 RGB 合成图（VRT → RGB → Byte）

为了让其他 agent（LLM agent、后端服务、工作流引擎）复用这套能力，需要将其从**进程内工具**升级为**标准化服务接口**。

---

## 方案对比

| 方案 | 调用方式 | 最佳适用场景 | 复杂度 | 外部依赖 |
|------|---------|------------|--------|---------|
| **A. MCP Server** | 任何支持 MCP 的 agent 通过 `tools/list` + `tools/call` 调用 | 让 LLM agent（Claude、Cursor、自研 agent）用自然语言驱动影像下载 | 中 | `mcp-go` SDK |
| **B. HTTP API Server** | 任意语言通过 HTTP POST/GET 调用 | 被后端服务、Python 脚本、工作流引擎集成 | 中 | 无（纯 `net/http`） |
| **C. Go Library** | 其他 Go 程序直接 `import` | 构建更大的 Go 地理数据管道，内嵌下载能力 | 低 | 无 |
| **D. 增强版 CLI** | 其他 agent `exec` 调用并解析 JSON stdout | 快速验证、同机部署、无需网络暴露 | 低 | 无 |

### 推荐组合

- **首选 A（MCP Server）**：如果核心诉求是让 LLM agent 自主决策"查哪块区域、下哪些波段"。
- **兼做 C（Go Library）**：把核心业务逻辑拆成可 import 的包，MCP Server 和内部服务共用同一套代码，避免两份实现。

---

## 方案 A：MCP Server

### 核心设计原则

1. **Tool 粒度适中**：不能太粗（LLM 无法中途纠错），也不能太细（调用次数爆炸）。
2. **纯结构化返回**：所有 Tool 返回 JSON，LLM 可直接解析做下一步决策。
3. **长任务支持**：下载可能持续数分钟，需要支持"提交-轮询"或"流式进度"模式。

### Tool 定义

#### `search_sentinel2`

搜索符合时空和云量条件的 Sentinel-2 影像。

**Input Schema**
```json
{
  "bbox": [116.2, 39.8, 116.6, 40.0],
  "start_date": "2026-01-01",
  "end_date": "2026-01-15",
  "max_cloud": 20.0,
  "bands": ["red", "green", "blue", "nir"],
  "limit": 20
}
```

**Output Schema**
```json
{
  "count": 5,
  "items": [
    {
      "id": "S2C_50TMK_20260111_0_L2A",
      "datetime": "2026-01-11T03:16:47Z",
      "cloud_cover": 0.4,
      "bbox": [115.816839, 39.655762, 117.115431, 40.65079],
      "assets": {
        "red":  {"href": "https://.../red.tif",  "type": "image/tiff"},
        "green": {"href": "https://.../green.tif", "type": "image/tiff"},
        "blue":  {"href": "https://.../blue.tif",  "type": "image/tiff"},
        "nir":   {"href": "https://.../nir.tif",   "type": "image/tiff"}
      }
    }
  ]
}
```

#### `download_sentinel2_bands`

下载指定影像的波段，支持并发和断点续传。

**Input Schema**
```json
{
  "item_id": "S2C_50TMK_20260111_0_L2A",
  "bands": ["red", "green", "blue"],
  "dest_dir": "./sentinel_data",
  "max_workers": 4,
  "max_retries": 3
}
```

**Output Schema（同步模式）**
```json
{
  "item_id": "S2C_50TMK_20260111_0_L2A",
  "status": "completed",
  "downloaded": ["S2C_50TMK_20260111_0_L2A_red.tif", "..."],
  "skipped": [],
  "failed": [],
  "dest_dir": "./sentinel_data"
}
```

**Output Schema（异步模式）**
```json
{
  "task_id": "task_abc123",
  "status": "queued",
  "message": "Download task submitted."
}
```

#### `get_download_status`

查询异步下载任务的进度。

**Input Schema**
```json
{
  "task_id": "task_abc123"
}
```

**Output Schema**
```json
{
  "task_id": "task_abc123",
  "status": "running",
  "progress_percent": 65,
  "completed_files": 2,
  "total_files": 4,
  "failed_files": 0
}
```

#### `build_sentinel2_rgb`

为已下载波段的影像构建 RGB 合成图（保留 `_RGB.tif` 和 `_byte.tif`）。

**Input Schema**
```json
{
  "item_id": "S2C_50TMK_20260111_0_L2A",
  "dest_dir": "./sentinel_data"
}
```

**Output Schema**
```json
{
  "item_id": "S2C_50TMK_20260111_0_L2A",
  "rgb_path": "./sentinel_data/S2C_50TMK_20260111_0_L2A_RGB.tif",
  "byte_path": "./sentinel_data/S2C_50TMK_20260111_0_L2A_byte.tif",
  "status": "completed"
}
```

### 长任务处理策略

MCP 协议原生是请求-响应模型，对长时间下载不够友好。两种策略可选：

| 策略 | 实现方式 | 优点 | 缺点 |
|------|---------|------|------|
| **同步阻塞** | Tool 内部等下载完成再返回 | 实现简单，LLM 一次对话即可拿到结果 | 超时风险大（单个文件 50-200MB），需设置超长 timeout |
| **异步轮询** | `download_sentinel2_bands` 返回 task_id，配合 `get_download_status` 轮询 | 稳定可靠，agent 可并发提交多个任务 | 需要多轮对话，实现稍复杂 |

**建议**：
- 默认使用**异步轮询**，提供 `task_id`
- 对极少量小文件（如预览图）可额外提供同步快捷模式

### 技术栈

- **传输层**：MCP over stdio（本地子进程）或 MCP over SSE（网络服务）
- **SDK**：`github.com/mark3labs/mcp-go`
- **业务层**：复用现有 `main.go` 逻辑，拆分为独立包（见"代码拆分"章节）

---

## 方案 B：HTTP API Server

### 端点设计

```
POST   /api/v1/search              → 搜索影像
POST   /api/v1/download            → 提交下载任务（异步）
GET    /api/v1/tasks/:id           → 查询任务进度
GET    /api/v1/tasks/:id/files     → 列出已下载文件
DELETE /api/v1/tasks/:id           → 取消/清理任务
POST   /api/v1/rgb/:item_id        → 构建 RGB
```

### 任务状态机

```
queued → running → completed
            ↓
         failed / cancelled
```

### 内存任务队列（无需 Redis）

由于任务主要是 I/O 密集型（下载大文件），用 Go 的 `sync.Map` + `sync.RWMutex` 管理任务状态即可：

```go
type TaskStore struct {
    mu    sync.RWMutex
    tasks map[string]*Task
}
```

### 技术栈

- **Web 框架**：纯 `net/http`（保持零外部依赖）
- **序列化**：`encoding/json`
- **并发控制**：worker pool（复用现有实现）

---

## 方案 C：Go Library

### 包拆分规划

将 `main.go` 单文件拆分为以下包，MCP Server 和 HTTP Server 共用：

```
sentinel-scraper/
├── cmd/
│   ├── sentinel-scraper/          ← 原 CLI 入口
│   ├── sentinel2-mcp/         ← MCP Server 入口
│   └── sentinel2-api/         ← HTTP API Server 入口
├── internal/
│   └── config/
│       └── config.go          ← Config、LoadConfig
├── pkg/
│   ├── stac/
│   │   └── client.go          ← SearchItems、STAC 类型定义
│   ├── download/
│   │   └── downloader.go      ← DownloadAsset、worker pool、进度上报
│   ├── rgb/
│   │   └── builder.go         ← BuildRGB、GDAL 工具调用
│   └── task/
│       └── store.go           ← 异步任务状态管理（仅 API/MCP 需要）
├── go.mod
└── README.md
```

### 核心包接口预览

**`pkg/stac`**
```go
package stac

type Client struct {
    BaseURL    string
    HTTPClient *http.Client
}

func (c *Client) Search(ctx context.Context, opts SearchOptions) (*ItemCollection, error)
```

**`pkg/download`**
```go
package download

type Downloader struct {
    MaxWorkers  int
    MaxRetries  int
    Timeout     time.Duration
    ProgressCb  func(itemID, band string, downloaded, total int64)
}

func (d *Downloader) Download(ctx context.Context, item Item, bands []string, destDir string) (*Result, error)
```

**`pkg/rgb`**
```go
package rgb

func Build(destDir, itemID string, keepIntermediate bool) (rgbPath, bytePath string, err error)
```

---

## 决策建议

| 如果你的目标是... | 选择方案 |
|------------------|---------|
| 让 Claude/Cursor/自研 LLM agent 通过对话驱动下载 | **A（MCP）+ C（Library）** |
| 被 Python/Java/Node 后端服务调用 | **B（HTTP API）+ C（Library）** |
| 快速验证，不想引入网络层 | **D（增强 CLI）**，输出 JSON 即可 |
| 以上都要 | **A + B + C**：Library 做核心，两个入口做适配层 |

---

## 下一步行动

1. **确认调用方**：你的 agent 是什么类型？（LLM agent / 后端服务 / 同机脚本）
2. **确认部署方式**：单机运行还是网络服务？
3. **确认长任务策略**：能否接受多轮轮询，还是需要一次调用等到底？

明确后可直接进入接口定义（OpenAPI / JSON Schema）和代码拆分实现。
