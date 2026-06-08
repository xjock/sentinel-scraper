package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var stderrMu sync.Mutex

type progressReader struct {
	r           io.Reader
	total       int64
	current     int64
	lastPercent int
	label       string
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.current += int64(n)
	if pr.total > 0 {
		percent := int(pr.current * 100 / pr.total)
		if percent >= pr.lastPercent+10 {
			stderrMu.Lock()
			fmt.Fprintf(os.Stderr, "  [%s] %3d%% (%s / %s)\n", pr.label, percent, formatBytes(pr.current), formatBytes(pr.total))
			stderrMu.Unlock()
			pr.lastPercent = percent
		}
	} else {
		// 未知总大小时,每 10 MB 打印一次
		if pr.current >= int64(pr.lastPercent)*10*1024*1024 {
			stderrMu.Lock()
			fmt.Fprintf(os.Stderr, "  [%s] downloaded %s\n", pr.label, formatBytes(pr.current))
			stderrMu.Unlock()
			pr.lastPercent++
		}
	}
	return n, err
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func parseContentRangeTotal(contentRange string) int64 {
	idx := strings.LastIndex(contentRange, "/")
	if idx < 0 {
		return 0
	}
	total, _ := strconv.ParseInt(contentRange[idx+1:], 10, 64)
	return total
}

// resumableDownload 执行带 Range 续传的 GET 下载。
//
// 行为:
//   - 若 destPath 已有部分内容,自动构造 Range 请求续传
//   - 处理 200 / 206 / 416 三态;200 + 已有完整文件 → 返回 skipped=true
//   - knownTotal>0 优先使用,否则按响应推导
//
// 不负责:MkdirAll、size mismatch 校验/回滚、rename、auth 之外的请求装饰
//
// 调用者保留:目标路径选择、auth 注入、最终 size 校验、retry 编排。
func resumableDownload(
	ctx context.Context,
	client *http.Client,
	url string,
	auth Authenticator,
	destPath string,
	label string,
	knownTotal int64,
) (finalSize int64, total int64, skipped bool, err error) {
	var offset int64
	if info, statErr := os.Stat(destPath); statErr == nil {
		offset = info.Size()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, 0, false, fmt.Errorf("create request: %w", err)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	if err := auth.Apply(req); err != nil {
		return 0, 0, false, fmt.Errorf("authenticate request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, false, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	filename := filepath.Base(destPath)

	switch resp.StatusCode {
	case http.StatusOK:
		total = knownTotal
		if total == 0 {
			total = resp.ContentLength
		}
		if offset > 0 {
			if total > 0 && offset == total {
				return offset, total, true, nil
			}
			// 服务器不支持 Range,从头重新下载（保留已有内容到 .bak）
			os.Remove(destPath + ".bak")
			if err := os.Rename(destPath, destPath+".bak"); err == nil {
				defer os.Remove(destPath + ".bak")
			} else {
				os.Remove(destPath)
			}
			offset = 0
		}
		f, err := os.Create(destPath)
		if err != nil {
			return 0, total, false, fmt.Errorf("create file: %w", err)
		}

		if total > 0 {
			fmt.Fprintf(os.Stderr, "  [downloading] %s (%s)\n", filename, formatBytes(total))
		} else {
			fmt.Fprintf(os.Stderr, "  [downloading] %s (unknown size)\n", filename)
		}
		pr := &progressReader{r: resp.Body, total: total, current: 0, label: label}
		if _, err := io.Copy(f, pr); err != nil {
			f.Close()
			return 0, total, false, fmt.Errorf("write file: %w", err)
		}
		if err := f.Close(); err != nil {
			return 0, total, false, fmt.Errorf("close file: %w", err)
		}

	case http.StatusPartialContent:
		total = knownTotal
		if total == 0 {
			total = parseContentRangeTotal(resp.Header.Get("Content-Range"))
		}
		if total == 0 && resp.ContentLength > 0 {
			total = offset + resp.ContentLength
		}
		f, err := os.OpenFile(destPath, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return 0, total, false, fmt.Errorf("open file for append: %w", err)
		}

		if total > 0 {
			fmt.Fprintf(os.Stderr, "  [resuming] %s (%s / %s, %s remaining)\n", filename, formatBytes(offset), formatBytes(total), formatBytes(total-offset))
		} else {
			fmt.Fprintf(os.Stderr, "  [resuming] %s from %s\n", filename, formatBytes(offset))
		}
		pr := &progressReader{r: resp.Body, total: total, current: offset, label: label}
		if _, err := io.Copy(f, pr); err != nil {
			f.Close()
			return 0, total, false, fmt.Errorf("write file: %w", err)
		}
		if err := f.Close(); err != nil {
			return 0, total, false, fmt.Errorf("close file: %w", err)
		}

	case http.StatusRequestedRangeNotSatisfiable:
		return offset, 0, true, nil

	default:
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, false, fmt.Errorf("HTTP %d for %s: %s", resp.StatusCode, url, string(body))
	}

	info, err := os.Stat(destPath)
	if err != nil {
		return 0, total, false, fmt.Errorf("stat file: %w", err)
	}
	return info.Size(), total, false, nil
}
