package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var stdinReader = bufio.NewReader(os.Stdin)

func readLine(prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := stdinReader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// getSettingsAuth returns the correct AuthConfig for the current source.
// It checks per-source fields first, then falls back to the legacy Auth field.
func getSettingsAuth(s *Settings) *AuthConfig {
	if s == nil {
		return nil
	}
	if s.CDSEAuth != nil && s.CDSEAuth.Username != "" {
		return s.CDSEAuth
	}
	if s.EarthdataAuth != nil && s.EarthdataAuth.Username != "" {
		return s.EarthdataAuth
	}
	return nil
}

// hasSavedAuth 判断已有 settings 中是否保存了任一完整凭据。
func hasSavedAuth(s *Settings) bool {
	if s == nil {
		return false
	}
	if s.CDSEAuth != nil && s.CDSEAuth.Username != "" && s.CDSEAuth.Password != "" {
		return true
	}
	if s.EarthdataAuth != nil && s.EarthdataAuth.Username != "" && s.EarthdataAuth.Password != "" {
		return true
	}
	return false
}

// promptCredentials 询问用户名和密码。如果 existingAuth 已有完整凭据，允许直接回车
// 跳过两个输入以保持原值不变；否则强制输入两项非空。
func promptCredentials(existingAuth *AuthConfig) (string, string, error) {
	saved := existingAuth != nil && existingAuth.Username != "" && existingAuth.Password != ""
	if saved {
		fmt.Printf("已保存凭据：%s（直接回车保持不变）\n", existingAuth.Username)
	}
	username, err := readLine("邮箱（用户名）: ")
	if err != nil {
		return "", "", err
	}
	password, err := readLine("密码: ")
	if err != nil {
		return "", "", err
	}
	if username == "" && password == "" {
		if saved {
			return existingAuth.Username, existingAuth.Password, nil
		}
		return "", "", fmt.Errorf("用户名和密码不能为空")
	}
	if username == "" || password == "" {
		return "", "", fmt.Errorf("用户名和密码必须同时提供")
	}
	return username, password, nil
}

func promptSatellite() (SatelliteType, error) {
	fmt.Println("\n选择卫星数据类型:")
	fmt.Println("  1) Sentinel-2 L2A（多光谱，支持云量过滤和 RGB 合成）")
	fmt.Println("  2) Sentinel-1 GRD（SAR 雷达，不受云影响，VV/VH 极化）")
	fmt.Println("  3) Sentinel-1 SLC（SAR 雷达，不受云影响，VV/VH 极化）")
	fmt.Println("  4) HLS（NASA 融合数据，30m，支持 RGB 合成）")
	fmt.Println()
	satChoice, err := readLine("选择 [1-4]: ")
	if err != nil {
		return "", err
	}
	switch satChoice {
	case "2":
		return SatS1GRD, nil
	case "3":
		return SatS1SLC, nil
	case "4":
		return SatHLS, nil
	default:
		return SatS2L2A, nil
	}
}

func setupAuthWizard() error {
	existing, _ := loadSettings()

	fmt.Println("=== sentinel-scraper 认证配置 ===")
	fmt.Println()
	fmt.Println("配置认证信息后，程序会根据你要下载的数据自动选择最佳来源。")
	fmt.Println()

	settings := &Settings{}
	if existing != nil {
		settings.CDSEAuth = existing.CDSEAuth
		settings.EarthdataAuth = existing.EarthdataAuth
	}

	// CDSE
	fmt.Println("--- CDSE (Copernicus Data Space) ---")
	fmt.Println("用于 S2/S1 的 CDSE STAC 和 CDSE OData 下载。")
	fmt.Println("访问 https://dataspace.copernicus.eu/ 注册账号。")
	fmt.Println()
	var existingCDSE *AuthConfig
	if existing != nil {
		existingCDSE = existing.CDSEAuth
	}
	username, password, err := promptCredentials(existingCDSE)
	if err != nil {
		return err
	}
	if username != "" && password != "" {
		settings.CDSEAuth = &AuthConfig{Username: username, Password: password}
	}

	// Earthdata
	fmt.Println()
	fmt.Println("--- Earthdata (NASA) ---")
	fmt.Println("用于 S1 的 ASF 下载和 HLS 数据下载。")
	fmt.Println("访问 https://urs.earthdata.nasa.gov 注册账号。")
	fmt.Println()
	var existingEarthdata *AuthConfig
	if existing != nil {
		existingEarthdata = existing.EarthdataAuth
	}
	username, password, err = promptCredentials(existingEarthdata)
	if err != nil {
		return err
	}
	if username != "" && password != "" {
		settings.EarthdataAuth = &AuthConfig{Username: username, Password: password}
	}

	if err := saveSettings(settings); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}
	fmt.Printf("\n配置已保存到: %s\n", settingsPath())
	fmt.Println("文件权限: 0600（仅所有者可读写）")
	return nil
}

// ---------- Settings & Web-based Setup ----------

func settingsPath() string {
	home := os.Getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			home = "."
		}
	}
	return filepath.Join(home, ".sentinel-scraper", "settings.json")
}

type Settings struct {
	CDSEAuth      *AuthConfig `json:"cdse_auth,omitempty"`      // CDSE-only credentials
	EarthdataAuth *AuthConfig `json:"earthdata_auth,omitempty"` // Earthdata-only credentials
	Auth          *AuthConfig `json:"auth,omitempty"`           // Legacy field, migrated to cdse_auth on load
}

func loadSettings() (*Settings, error) {
	path := settingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read settings: %w", err)
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	// Migrate legacy auth field to cdse_auth so old credentials aren't lost.
	if s.Auth != nil && s.Auth.Username != "" && (s.CDSEAuth == nil || s.CDSEAuth.Username == "") {
		s.CDSEAuth = s.Auth
	}
	return &s, nil
}

func saveSettings(s *Settings) error {
	path := settingsPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Persist only the new per-source fields; legacy auth is dropped.
	type settingsSave struct {
		CDSEAuth      *AuthConfig `json:"cdse_auth,omitempty"`
		EarthdataAuth *AuthConfig `json:"earthdata_auth,omitempty"`
	}
	data, err := json.MarshalIndent(settingsSave{
		CDSEAuth:      s.CDSEAuth,
		EarthdataAuth: s.EarthdataAuth,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func needsSetup() bool {
	_, err := os.Stat(settingsPath())
	return os.IsNotExist(err)
}

func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	return exec.Command(cmd, args...).Start()
}

const setupHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>sentinel-scraper 认证配置</title>
<style>
  * { box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    background: #f5f7fa;
    margin: 0;
    padding: 40px 20px;
    display: flex;
    justify-content: center;
  }
  .container {
    background: #fff;
    border-radius: 12px;
    box-shadow: 0 2px 12px rgba(0,0,0,0.08);
    max-width: 480px;
    width: 100%;
    padding: 32px;
  }
  h1 { margin: 0 0 8px; font-size: 22px; color: #1a1a2e; }
  p.desc { margin: 0 0 24px; color: #666; font-size: 14px; }
  .field { margin-bottom: 16px; }
  label {
    display: block;
    font-size: 13px;
    font-weight: 600;
    color: #444;
    margin-bottom: 6px;
  }
  input[type="text"], input[type="password"] {
    width: 100%;
    padding: 10px 12px;
    border: 1px solid #d1d5db;
    border-radius: 8px;
    font-size: 14px;
    background: #fafbfc;
  }
  input:focus {
    outline: none;
    border-color: #3b82f6;
    background: #fff;
  }
  .hint {
    font-size: 12px;
    color: #888;
    margin-top: 4px;
  }
  .panel {
    border-left: 4px solid #3b82f6;
    padding: 16px;
    background: #f8fafc;
    border-radius: 0 8px 8px 0;
    margin-bottom: 20px;
  }
  .panel h3 { margin: 0 0 12px; font-size: 15px; color: #1e3a5f; }
  .steps { margin-bottom: 16px; }
  .steps p { margin: 0 0 8px; font-size: 13px; color: #555; line-height: 1.5; }
  .steps a { color: #3b82f6; }
  button {
    width: 100%;
    padding: 12px;
    background: #3b82f6;
    color: #fff;
    border: none;
    border-radius: 8px;
    font-size: 15px;
    font-weight: 600;
    cursor: pointer;
    margin-top: 8px;
  }
  button:hover { background: #2563eb; }
</style>
</head>
<body>
<div class="container">
  <h1>sentinel-scraper 认证配置</h1>
  <p class="desc">填写认证信息后，程序会根据你要下载的数据自动选择最佳来源。来源和数据类型在 config.json 中配置。</p>
  <form method="POST" action="/">
    <div class="panel">
      <h3>CDSE (Copernicus Data Space)</h3>
      <div class="steps">
        <p>用于 Sentinel-2/Sentinel-1 的 CDSE STAC 和 CDSE OData 下载。</p>
        <p>访问 <a href="https://dataspace.copernicus.eu/" target="_blank">dataspace.copernicus.eu</a> 注册账号。</p>
      </div>
      <div class="field">
        <label>邮箱（用户名）</label>
        <input type="text" name="cdse_username" placeholder="{{if .HasExistingCDSEAuth}}留空保持不变（{{.ExistingCDSEUsername}}）{{else}}your@email.com{{end}}">
      </div>
      <div class="field">
        <label>密码</label>
        <input type="password" name="cdse_password" placeholder="{{if .HasExistingCDSEAuth}}留空保持不变{{else}}CDSE 登录密码{{end}}">
      </div>
      {{if .HasExistingCDSEAuth}}<p class="hint">已保存 CDSE 凭据：{{.ExistingCDSEUsername}}（留空保持不变）。</p>{{else}}<p class="hint">使用 CDSE 登录邮箱和密码，密码保存在本地。</p>{{end}}
    </div>

    <div class="panel" style="border-left-color: #e11d48; background: #fff1f2;">
      <h3 style="color: #be123c;">Earthdata (NASA)</h3>
      <div class="steps">
        <p>用于 Sentinel-1 的 ASF 下载和 HLS 数据下载。</p>
        <p>访问 <a href="https://urs.earthdata.nasa.gov/" target="_blank">urs.earthdata.nasa.gov</a> 注册账号。</p>
      </div>
      <div class="field">
        <label>用户名</label>
        <input type="text" name="earthdata_username" placeholder="{{if .HasExistingEarthdataAuth}}留空保持不变（{{.ExistingEarthdataUsername}}）{{else}}your_username{{end}}">
      </div>
      <div class="field">
        <label>密码</label>
        <input type="password" name="earthdata_password" placeholder="{{if .HasExistingEarthdataAuth}}留空保持不变{{else}}Earthdata 密码{{end}}">
      </div>
      {{if .HasExistingEarthdataAuth}}<p class="hint">已保存 Earthdata 凭据：{{.ExistingEarthdataUsername}}（留空保持不变）。</p>{{else}}<p class="hint">使用 Earthdata 登录用户名和密码，密码保存在本地。</p>{{end}}
    </div>

    <button type="submit">保存</button>
  </form>
</div>
</body>
</html>`

const successHTML = `<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"><title>配置完成</title>
<style>
  body { font-family: sans-serif; background: #f5f7fa; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; }
  .box { background: #fff; padding: 40px; border-radius: 12px; text-align: center; box-shadow: 0 2px 12px rgba(0,0,0,0.08); }
  h1 { color: #10b981; margin: 0 0 12px; }
  p { color: #666; margin: 0; }
</style>
</head>
<body>
<div class="box">
  <h1>配置完成</h1>
  <p>可以关闭此页面，程序将继续运行。</p>
</div>
</body>
</html>`

type setupPageData struct {
	HasExistingCDSEAuth       bool
	ExistingCDSEUsername      string
	HasExistingEarthdataAuth  bool
	ExistingEarthdataUsername string
}

var setupTmpl = template.Must(template.New("setup").Parse(setupHTML))

func runSetupWizard() (*Settings, error) {
	existing, _ := loadSettings()
	cdseSaved := existing != nil && existing.CDSEAuth != nil && existing.CDSEAuth.Username != ""
	earthdataSaved := existing != nil && existing.EarthdataAuth != nil && existing.EarthdataAuth.Username != ""

	done := make(chan *Settings, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Bad request", http.StatusBadRequest)
				return
			}

			settings := &Settings{}
			if existing != nil {
				settings.CDSEAuth = existing.CDSEAuth
				settings.EarthdataAuth = existing.EarthdataAuth
			}

			user := strings.TrimSpace(r.FormValue("cdse_username"))
			pass := strings.TrimSpace(r.FormValue("cdse_password"))
			if user != "" && pass != "" {
				settings.CDSEAuth = &AuthConfig{Username: user, Password: pass}
			}

			user = strings.TrimSpace(r.FormValue("earthdata_username"))
			pass = strings.TrimSpace(r.FormValue("earthdata_password"))
			if user != "" && pass != "" {
				settings.EarthdataAuth = &AuthConfig{Username: user, Password: pass}
			}

			if err := saveSettings(settings); err != nil {
				http.Error(w, "Failed to save settings", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, successHTML)
			select {
			case done <- settings:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := setupPageData{
			HasExistingCDSEAuth:      cdseSaved,
			HasExistingEarthdataAuth: earthdataSaved,
		}
		if cdseSaved {
			data.ExistingCDSEUsername = existing.CDSEAuth.Username
		}
		if earthdataSaved {
			data.ExistingEarthdataUsername = existing.EarthdataAuth.Username
		}
		if err := setupTmpl.Execute(w, data); err != nil {
			http.Error(w, "render failed", http.StatusInternalServerError)
		}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	fmt.Printf("正在浏览器中打开配置页面：%s\n", url)
	if err := openBrowser(url); err != nil {
		fmt.Printf("请手动在浏览器中打开以下地址：%s\n", url)
	}

	select {
	case settings := <-done:
		return settings, nil
	case <-time.After(10 * time.Minute):
		return nil, fmt.Errorf("setup timed out after 10 minutes")
	}
}
