package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// removeWithRetry 在 Windows 上 GDAL 进程刚退出时文件句柄可能未立即释放，
// 短暂重试后再删除，避免中间文件残留。
func removeWithRetry(path string) {
	for i := 0; i < 5; i++ {
		if err := os.Remove(path); err == nil || os.IsNotExist(err) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func findGDALTool(name string) string {
	exeName := name + ".exe"
	if _, err := os.Stat(exeName); err == nil {
		// 检查关键 DLL 是否也在当前目录，防止用户只拷贝了 exe 导致 0xc0000135
		if _, err := os.Stat("gdal305.dll"); err == nil {
			absPath, _ := filepath.Abs(exeName)
			return absPath
		}
	}
	return name
}

func gdalEnv() []string {
	env := os.Environ()
	if _, err := os.Stat("share/proj"); err == nil {
		projDir, _ := filepath.Abs("share/proj")
		env = append(env, "PROJ_DATA="+projDir)
	} else if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		projPath := filepath.Join(exeDir, "share", "proj")
		if _, err := os.Stat(projPath); err == nil {
			env = append(env, "PROJ_DATA="+projPath)
		}
	}
	return env
}

func BuildRGB(destDir string, itemID string) error {
	byteName := fmt.Sprintf("%s_byte.tif", itemID)
	bytePath := filepath.Join(destDir, byteName)
	rgbaName := fmt.Sprintf("%s_rgba.tif", itemID)
	rgbaPath := filepath.Join(destDir, rgbaName)

	if _, err := os.Stat(rgbaPath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", rgbaName)
		return nil
	}

	rgbPath := filepath.Join(destDir, fmt.Sprintf("%s_RGB.tif", itemID))

	// 若已存在未renew的 _byte.tif，直接复用，不重新合成
	if _, err := os.Stat(bytePath); err == nil {
		fmt.Printf("  [reuse] %s, retrying rgba\n", byteName)
		if err := buildRGBA(bytePath, rgbaPath, destDir); err != nil {
			fmt.Fprintf(os.Stderr, "  [rgba skip] %s: %v\n", itemID, err)
			removeWithRetry(bytePath)
			removeWithRetry(rgbPath)
			return nil
		}
		removeWithRetry(bytePath)
		removeWithRetry(rgbPath)
		fmt.Printf("  [rgba] %s\n", rgbaName)
		return nil
	}

	bands := []string{"red", "green", "blue"}
	bandPaths := []string{}
	for _, band := range bands {
		p := filepath.Join(destDir, fmt.Sprintf("%s_%s.tif", itemID, band))
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("missing band %s: %w", band, err)
		}
		bandPaths = append(bandPaths, p)
	}

	vrtPath := filepath.Join(destDir, fmt.Sprintf("%s_rgb.vrt", itemID))
	buildCmd := exec.Command(findGDALTool("gdalbuildvrt"), append([]string{"-separate", vrtPath}, bandPaths...)...)
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdalbuildvrt"), strings.Join(append([]string{"-separate", vrtPath}, bandPaths...), " "))
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	buildCmd.Env = gdalEnv()
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("gdalbuildvrt failed: %w", err)
	}
	defer os.Remove(vrtPath)

	transArgs := []string{vrtPath, rgbPath}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdal_translate"), strings.Join(transArgs, " "))
	transCmd := exec.Command(findGDALTool("gdal_translate"), transArgs...)
	transCmd.Stdout = os.Stdout
	transCmd.Stderr = os.Stderr
	transCmd.Env = gdalEnv()
	if err := transCmd.Run(); err != nil {
		return fmt.Errorf("gdal_translate failed: %w", err)
	}

	// 固定 0-3000 拉伸到 0-255，0 保留为 nodata
	args := []string{
		"-ot", "Byte",
		"-a_nodata", "0",
		"-scale_1", "0", "3000", "0", "255",
		"-scale_2", "0", "3000", "0", "255",
		"-scale_3", "0", "3000", "0", "255",
		rgbPath, bytePath,
	}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdal_translate"), strings.Join(args, " "))

	byteCmd := exec.Command(findGDALTool("gdal_translate"), args...)
	byteCmd.Stdout = os.Stdout
	byteCmd.Stderr = os.Stderr
	byteCmd.Env = gdalEnv()
	if err := byteCmd.Run(); err != nil {
		return fmt.Errorf("gdal_translate to byte failed: %w", err)
	}

	fmt.Printf("  [rgb] %s  %s\n", rgbPath, bytePath)

	if err := buildRGBA(bytePath, rgbaPath, destDir); err != nil {
		fmt.Fprintf(os.Stderr, "  [rgba skip] %s: %v\n", itemID, err)
		removeWithRetry(bytePath)
		removeWithRetry(rgbPath)
		return nil
	}

	removeWithRetry(bytePath)
	removeWithRetry(rgbPath)
	fmt.Printf("  [rgba] %s\n", rgbaName)
	return nil
}

// buildRGBByte 接受显式 R/G/B 波段路径，直接生成拉伸后的 byte 合成图。
// 中间 VRT 写入 workDir，不产生独立的 _RGB.tif。
func buildRGBByte(redPath, greenPath, bluePath, bytePath, workDir string) error {
	vrtPath := filepath.Join(workDir, "rgb.vrt")
	buildArgs := []string{"-separate", vrtPath, redPath, greenPath, bluePath}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdalbuildvrt"), strings.Join(buildArgs, " "))
	buildCmd := exec.Command(findGDALTool("gdalbuildvrt"), buildArgs...)
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	buildCmd.Env = gdalEnv()
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("gdalbuildvrt failed: %w", err)
	}

	args := []string{
		"-ot", "Byte",
		"-a_nodata", "0",
		"-scale_1", "0", "3000", "0", "255",
		"-scale_2", "0", "3000", "0", "255",
		"-scale_3", "0", "3000", "0", "255",
		vrtPath, bytePath,
	}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdal_translate"), strings.Join(args, " "))

	byteCmd := exec.Command(findGDALTool("gdal_translate"), args...)
	byteCmd.Stdout = os.Stdout
	byteCmd.Stderr = os.Stderr
	byteCmd.Env = gdalEnv()
	if err := byteCmd.Run(); err != nil {
		return fmt.Errorf("gdal_translate to byte failed: %w", err)
	}
	return nil
}

func getImageSize(tifPath string) (int, int, error) {
	cmd := exec.Command(findGDALTool("gdalinfo"), tifPath)
	cmd.Env = gdalEnv()
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, fmt.Errorf("gdalinfo failed: %w", err)
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Size is ") {
			var w, h int
			if _, err := fmt.Sscanf(line, "Size is %d, %d", &w, &h); err == nil {
				return w, h, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("could not parse image size from gdalinfo")
}

// getImageExtent 解析 gdalinfo 输出，返回投影坐标系的 extent (xmin, ymin, xmax, ymax)
func getImageExtent(tifPath string) (float64, float64, float64, float64, error) {
	cmd := exec.Command(findGDALTool("gdalinfo"), tifPath)
	cmd.Env = gdalEnv()
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("gdalinfo failed: %w", err)
	}
	var xmin, ymax, xmax, ymin float64
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Upper Left  (") {
			fmt.Sscanf(line, "Upper Left  ( %f, %f)", &xmin, &ymax)
		} else if strings.HasPrefix(line, "Lower Right (") {
			fmt.Sscanf(line, "Lower Right ( %f, %f)", &xmax, &ymin)
		}
	}
	if xmin == 0 && ymin == 0 && xmax == 0 && ymax == 0 {
		return 0, 0, 0, 0, fmt.Errorf("could not parse extent from gdalinfo")
	}
	return xmin, ymin, xmax, ymax, nil
}

// buildRGBA 对 bytePath 跑 gdal_trace_outline → gdal_rasterize → gdalwarp → gdal_merge_simple，
// 把 RGB + Alpha 四通道图写到 outputPath。中间产物落在 workDir 并清理。
// 原 bytePath 不在此函数中删除，由调用者负责。失败时不会留下半成品 outputPath。
func buildRGBA(bytePath, outputPath, workDir string) error {
	base := strings.TrimSuffix(filepath.Base(bytePath), filepath.Ext(bytePath))
	maskShpBase := filepath.Join(workDir, base+"_mask")
	maskShpPath := maskShpBase + ".shp"
	maskTifPath := filepath.Join(workDir, base+"_mask.tif")
	maskCropPath := filepath.Join(workDir, base+"_mask_crop.tif")
	cropPath := filepath.Join(workDir, base+"_crop.tif")

	// 1) gdal_trace_outline
	traceArgs := []string{bytePath, "-ndv", "0", "-min-ring-area", "10000000", "-out-cs", "en", "-ogr-out", maskShpPath}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdal_trace_outline"), strings.Join(traceArgs, " "))
	traceCmd := exec.Command(findGDALTool("gdal_trace_outline"), traceArgs...)
	traceCmd.Stdout = os.Stdout
	traceCmd.Stderr = os.Stderr
	traceCmd.Env = gdalEnv()
	if err := traceCmd.Run(); err != nil {
		return fmt.Errorf("gdal_trace_outline failed: %w", err)
	}

	// 2) gdal_rasterize（extent 和分辨率与 byte.tif 完全一致）
	w, h, err := getImageSize(bytePath)
	if err != nil {
		return err
	}
	xmin, ymin, xmax, ymax, err := getImageExtent(bytePath)
	if err != nil {
		return err
	}
	rastArgs := []string{
		"-ot", "Byte", "-a_nodata", "0", "-burn", "255",
		"-te", fmt.Sprintf("%f", xmin), fmt.Sprintf("%f", ymin), fmt.Sprintf("%f", xmax), fmt.Sprintf("%f", ymax),
		"-ts", fmt.Sprintf("%d", w), fmt.Sprintf("%d", h),
		maskShpPath, maskTifPath,
	}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdal_rasterize"), strings.Join(rastArgs, " "))
	rastCmd := exec.Command(findGDALTool("gdal_rasterize"), rastArgs...)
	rastCmd.Stdout = os.Stdout
	rastCmd.Stderr = os.Stderr
	rastCmd.Env = gdalEnv()
	if err := rastCmd.Run(); err != nil {
		return fmt.Errorf("gdal_rasterize failed: %w", err)
	}

	// 3) gdalwarp byte.tif → crop.tif
	warpArgs := []string{"-overwrite", "-cutline", maskShpPath, "-crop_to_cutline", bytePath, cropPath, "--config", "CHECK_DISK_FREE_SPACE", "NO"}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdalwarp"), strings.Join(warpArgs, " "))
	warpCmd := exec.Command(findGDALTool("gdalwarp"), warpArgs...)
	warpCmd.Stdout = os.Stdout
	warpCmd.Stderr = os.Stderr
	warpCmd.Env = gdalEnv()
	if err := warpCmd.Run(); err != nil {
		return fmt.Errorf("gdalwarp failed: %w", err)
	}

	// 4) gdalwarp mask.tif → mask_crop.tif（裁到与 crop.tif 同尺寸）
	maskWarpArgs := []string{"-overwrite", "-cutline", maskShpPath, "-crop_to_cutline", maskTifPath, maskCropPath, "--config", "CHECK_DISK_FREE_SPACE", "NO"}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdalwarp"), strings.Join(maskWarpArgs, " "))
	maskWarpCmd := exec.Command(findGDALTool("gdalwarp"), maskWarpArgs...)
	maskWarpCmd.Stdout = os.Stdout
	maskWarpCmd.Stderr = os.Stderr
	maskWarpCmd.Env = gdalEnv()
	if err := maskWarpCmd.Run(); err != nil {
		return fmt.Errorf("gdalwarp mask failed: %w", err)
	}

	// 5) gdal_merge_simple
	mergeArgs := []string{"-in", cropPath, "-in", maskCropPath, "-out", outputPath}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdal_merge_simple"), strings.Join(mergeArgs, " "))
	mergeCmd := exec.Command(findGDALTool("gdal_merge_simple"), mergeArgs...)
	mergeCmd.Stdout = os.Stdout
	mergeCmd.Stderr = os.Stderr
	mergeCmd.Env = gdalEnv()
	if err := mergeCmd.Run(); err != nil {
		removeWithRetry(outputPath)
		return fmt.Errorf("gdal_merge_simple failed: %w", err)
	}

	// 清理中间产物
	removeWithRetry(maskShpPath)
	removeWithRetry(maskShpBase + ".shx")
	removeWithRetry(maskShpBase + ".dbf")
	removeWithRetry(maskShpBase + ".prj")
	removeWithRetry(maskShpBase + ".cpg")
	removeWithRetry(maskTifPath)
	removeWithRetry(cropPath)
	removeWithRetry(maskCropPath)
	return nil
}
