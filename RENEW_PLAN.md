# Renew Pipeline 规划（RGBA 四通道方案）

把 `gdal_trace_outline → gdal_rasterize → gdalwarp → gdal_merge_simple` 接进 **OData 分支**的 RGB 合成末端，自动修复 `_byte.tif` 内部 nodata 像素，最终产物为 `*_rgba.tif`（四通道：R、G、B、Alpha）。

> **范围（已锁定）**：本次只动 OData 分支（即 `processODataProduct` / `buildRGBByte` 这条链路），Earth Search 与 CDSE STAC 模式（`BuildRGB`）保持不动。

## 1. Pipeline 四步

针对一个已生成的 `<destDir>/<productName>_byte.tif`：

```sh
# 1) 用 _byte.tif 自身的有效像素提取外轮廓（Shapefile）
#    -ndv 0 表示把 0 视为 nodata
#    -min-ring-area 10000000 过滤掉小碎斑
#    -out-cs ll 输出坐标系为经纬度（与 gdalwarp -cutline 兼容）
gdal_trace_outline xxx_byte.tif -ndv 0 -min-ring-area 10000000 -out-cs ll -ogr-out xxx_mask.shp

# 2) 把 shapefile 栅格化成 0/255 的单波段 mask
#    -ot Byte 输出 8bit
#    -a_nodata 0 0 值表示透明
#    -a val 用属性字段 val 填充（shapefile 中有效区域为 255）
#    -ts xsize ysize 尺寸与 xxx_byte.tif 一致
gdal_rasterize -ot Byte -a_nodata 0 -a val -ts xsize ysize xxx_mask.shp xxx_mask.tif

# 3) 按轮廓裁掉外侧无数据区域，生成真实范围的 xxx_crop.tif
#    -cutline 指定裁剪边界
#    -crop_to_cutline 把输出范围收缩到边界框
gdalwarp -cutline xxx_mask.shp -crop_to_cutline xxx_byte.tif xxx_crop.tif --config CHECK_DISK_FREE_SPACE NO

# 4) 把 xxx_crop.tif（3 波段 RGB）与 xxx_mask.tif（1 波段 Alpha）合并为 RGBA 四通道
gdal_merge_simple -in xxx_crop.tif -in xxx_mask.tif -out xxx_rgba.tif

# 5) 成功后：删除中间产物（xxx_byte.tif、xxx_mask.shp 全套伴生文件、xxx_mask.tif、xxx_crop.tif）
#    只保留最终 xxx_rgba.tif
```

为何不需要 `pkRenew`：

- 第 3 步 `gdalwarp -crop_to_cutline` 已经把图像裁到真实数据边界，外侧 nodata 被彻底裁掉
- 第 4 步 `gdal_merge_simple` 把 Alpha 通道（mask）贴进第四波段，渲染时自动透明
- 内部零星 nodata 像素在裁剪后如果仍落在边界内，由 Alpha 通道控制透明度
- 整个流程不再依赖 `pkRenew` 的像素级填充逻辑

## 2. 代码层接入点

只动两个文件：

| 文件 | 改动 |
|---|---|
| `gdal.go` | 新增 `buildRGBA(bytePath, outputPath, workDir) error`，封装上述四步 |
| `odata.go` | `processODataProduct` 在 `buildRGBByte` 之后调用 `buildRGBA`，处理 skip-if-exists 与失败回退 |

`BuildRGB`（Earth Search + CDSE STAC）**不动**。

## 3. 文件命名 & 中间产物

成品落到 `destDir`：

- `<productName>_rgba.tif`：最终四通道 RGBA 成品
- `<productName>_byte.tif`：合成结果，rgba 成功后删除；失败时保留

中间产物落到 `workDir`（即 `<destDir>/<productName>_extract/` 或独立临时目录，处理结束 `os.RemoveAll`）：

- `<productName>_mask.shp/.shx/.dbf/.prj/.cpg`
- `<productName>_mask.tif`
- `<productName>_crop.tif`

## 4. skip-if-exists 行为

`processODataProduct` 入口：

| 既有文件 | 行为 |
|---|---|
| 已有 `_rgba.tif` | 整个步骤跳过 |
| 仅有 `_byte.tif`（上次 rgba 合成失败） | 复用 `_byte.tif`，重跑 `buildRGBA`，不重新解压 |
| 都没有 | 走完整流程：解压 → buildRGBByte → buildRGBA |

## 5. 错误处理（最终）

rgba 合成是「锦上添花」步骤，不应该让主流程失败：

- `gdal_trace_outline` / `gdal_rasterize` / `gdalwarp` / `gdal_merge_simple` 任一步失败（缺失或非零退出）→ 打印 `[rgba skip] <productName>: <err>`，**保留原始 `_byte.tif`**，不生成 `_rgba.tif`，`processODataProduct` 仍返回 `nil`
- 成功 → 打印 `[rgba] <productName>_rgba.tif`，并 `os.Remove(原 _byte.tif)` 及中间产物

## 6. 决策点（已锁定）

| # | 决策点 | 结论 |
|---|---|---|
| A | outline 文件格式 | **Shapefile**（默认 driver 输出，全部伴生文件随后清理） |
| B | 最终落盘文件 | **只保留 `_rgba.tif`**（成功后删除 `_byte.tif` 及所有中间产物） |
| C | 失败行为 | **降级警告，保留原 `_byte.tif`** |
| D | 工具查找 | **复用 `findGDALTool`**（Windows 当前目录 + DLL 检测，Linux 走 PATH） |
| E | 命令名 | `gdal_trace_outline`、`gdal_rasterize`、`gdalwarp`、`gdal_merge_simple` |
| F | `gdal_trace_outline` 的 `-ndv` | **显式加 `-ndv 0`** |
| G | `gdalwarp` 额外配置 | **`--config CHECK_DISK_FREE_SPACE NO`** |
| H | 范围 | **只对 OData 分支做 RGBA 合成**，BuildRGB 不动 |

## 7. 影响面 & 验证

- 改动文件：`gdal.go`（新增 1 个函数）、`odata.go`（修改 `processODataProduct`）
- 影响：仅 OData 分支生成的 `_rgba.tif` 为四通道带 Alpha 掩膜，渲染时自动处理 nodata 透明
- 验证：`go build ./...`、`go vet ./...`，再用一个 OData 产品跑通看 `[rgba]` 日志和最终文件

## 8. 工具依赖

| 工具 | 来源 | 必需性 |
|---|---|---|
| `gdal_trace_outline` | dans-gdal-scripts | 缺失 → rgba 合成跳过，主流程不受影响 |
| `gdal_rasterize` | GDAL 核心 | 已假定可用（其他流程也在用 GDAL） |
| `gdalwarp` | GDAL 核心 | 已假定可用 |
| `gdal_merge_simple` | dans-gdal-scripts | 缺失 → rgba 合成跳过，主流程不受影响 |
