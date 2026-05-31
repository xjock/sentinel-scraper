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

// hasSavedAuth 判断已有 settings 中是否保存了完整的用户名/密码。
func hasSavedAuth(s *Settings) bool {
	return s != nil && s.Auth != nil && s.Auth.Username != "" && s.Auth.Password != ""
}

// promptCredentials 询问用户名和密码。如果 existing 已有完整凭据，允许直接回车
// 跳过两个输入以保持原值不变；否则强制输入两项非空。
func promptCredentials(existing *Settings) (string, string, error) {
	saved := hasSavedAuth(existing)
	if saved {
		fmt.Printf("已保存凭据：%s（直接回车保持不变）\n", existing.Auth.Username)
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
			return existing.Auth.Username, existing.Auth.Password, nil
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
	fmt.Println()
	satChoice, err := readLine("选择 [1-3]: ")
	if err != nil {
		return "", err
	}
	switch satChoice {
	case "2":
		return SatS1GRD, nil
	case "3":
		return SatS1SLC, nil
	default:
		return SatS2L2A, nil
	}
}

func setupAuthWizard() error {
	existing, _ := loadSettings()

	fmt.Println("=== sentinel-scraper 认证配置 ===")
	fmt.Println()
	fmt.Println("选择数据源:")
	fmt.Println("  1) Earth Search — 无需认证，需翻墙")
	fmt.Println("  2) CDSE STAC API — 按波段下载，需翻墙")
	fmt.Println("  3) CDSE OData API — 整景 ZIP 下载，无需翻墙")
	fmt.Println("  4) 自定义 STAC API")
	fmt.Println()

	choice, err := readLine("选择 [1-4]: ")
	if err != nil {
		return err
	}

	switch choice {
	case "1":
		sat, err := promptSatellite()
		if err != nil {
			return err
		}
		sc := satelliteConfigs[sat]
		settings := &Settings{
			Source:     "earth_search",
			STACURL:    EarthSearchURL,
			Collection: sc.Collection,
			Satellite:  string(sat),
		}
		if err := saveSettings(settings); err != nil {
			return fmt.Errorf("保存配置失败: %w", err)
		}
		fmt.Printf("\n配置已保存到: %s\n", settingsPath())
		fmt.Println("Earth Search 为默认数据源，无需认证。")

	case "2":
		sat, err := promptSatellite()
		if err != nil {
			return err
		}
		sc := satelliteConfigs[sat]
		fmt.Println("\n--- CDSE STAC 配置 ---")
		if sat == SatS2L2A {
			fmt.Println("按波段下载（red/green/blue/nir 等），支持断点续传和 RGB 合成。")
		} else {
			fmt.Println("按极化下载（vv/vh 等），SAR 数据不受云影响。")
		}
		fmt.Println("访问 https://dataspace.copernicus.eu/ 注册账号。")
		fmt.Println()
		username, password, err := promptCredentials(existing)
		if err != nil {
			return err
		}
		settings := &Settings{
			Source:     "cdse",
			STACURL:    "https://stac.dataspace.copernicus.eu/v1",
			Collection: sc.CDSECollection,
			Satellite:  string(sat),
			Auth:       &AuthConfig{Username: username, Password: password},
		}
		if err := saveSettings(settings); err != nil {
			return fmt.Errorf("保存配置失败: %w", err)
		}
		fmt.Printf("\n配置已保存到: %s\n", settingsPath())
		fmt.Println("文件权限: 0600（仅所有者可读写）")

	case "3":
		sat, err := promptSatellite()
		if err != nil {
			return err
		}
		fmt.Println("\n--- CDSE OData 配置 ---")
		if sat == SatS2L2A {
			fmt.Println("整景 ZIP 下载（包含所有波段和元数据），适合需要完整产品的场景。")
		} else {
			fmt.Println("整景 ZIP 下载（SAR 原始数据），不受云影响。")
		}
		fmt.Println("访问 https://dataspace.copernicus.eu/ 注册账号。")
		fmt.Println()
		username, password, err := promptCredentials(existing)
		if err != nil {
			return err
		}
		settings := &Settings{
			Source:    "cdse_odata",
			Satellite: string(sat),
			Auth:      &AuthConfig{Username: username, Password: password},
		}
		if err := saveSettings(settings); err != nil {
			return fmt.Errorf("保存配置失败: %w", err)
		}
		fmt.Printf("\n配置已保存到: %s\n", settingsPath())
		fmt.Println("文件权限: 0600（仅所有者可读写）")

	case "4":
		fmt.Println("\n--- 自定义 STAC API 配置 ---")
		stacURL, err := readLine("STAC API 地址: ")
		if err != nil {
			return err
		}
		collection, err := readLine("Collection 名称: ")
		if err != nil {
			return err
		}
		if stacURL == "" || collection == "" {
			return fmt.Errorf("地址和名称不能为空")
		}
		sat := ParseSatelliteType(collection)
		settings := &Settings{
			Source:     "custom",
			STACURL:    stacURL,
			Collection: collection,
			Satellite:  string(sat),
		}
		if err := saveSettings(settings); err != nil {
			return fmt.Errorf("保存配置失败: %w", err)
		}
		fmt.Printf("\n配置已保存到: %s\n", settingsPath())
		fmt.Println("文件权限: 0600（仅所有者可读写）")

	default:
		return fmt.Errorf("无效选择，请重新运行并选择 1、2、3 或 4")
	}
	return nil
}

// ---------- Settings & Web-based Setup ----------

func settingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".sentinel-scraper", "settings.json")
}

type Settings struct {
	Source     string      `json:"source"`
	Satellite  string      `json:"satellite,omitempty"`
	STACURL    string      `json:"stac_url,omitempty"`
	Collection string      `json:"collection,omitempty"`
	Auth       *AuthConfig `json:"auth,omitempty"`
}

func loadSettings() (*Settings, error) {
	path := settingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveSettings(s *Settings) error {
	path := settingsPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
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
<title>sentinel-scraper Setup</title>
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
  input[type="text"], input[type="password"], select {
    width: 100%;
    padding: 10px 12px;
    border: 1px solid #d1d5db;
    border-radius: 8px;
    font-size: 14px;
    background: #fafbfc;
  }
  input:focus, select:focus {
    outline: none;
    border-color: #3b82f6;
    background: #fff;
  }
  .hint {
    font-size: 12px;
    color: #888;
    margin-top: 4px;
  }
  .hidden { display: none; }
  .panel {
    border-left: 4px solid #3b82f6;
    padding: 16px;
    background: #f8fafc;
    border-radius: 0 8px 8px 0;
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
  .earth { border-left: 4px solid #10b981; padding-left: 12px; background: #f0fdf4; border-radius: 0 8px 8px 0; }
  .cdse { border-left: 4px solid #3b82f6; padding-left: 12px; background: #eff6ff; border-radius: 0 8px 8px 0; }
  .custom { border-left: 4px solid #f59e0b; padding-left: 12px; background: #fffbeb; border-radius: 0 8px 8px 0; }
</style>
</head>
<body>
<div class="container">
  <h1>sentinel-scraper 数据源配置</h1>
  <p class="desc">选择数据源、卫星类型和认证方式。</p>
  <form method="POST" action="/">
    <div class="field">
      <label for="source">数据源</label>
      <select id="source" name="source" onchange="onSourceChange()">
        <option value="cdse_odata">CDSE OData API（整景 ZIP 下载，无需翻墙）</option>
        <option value="cdse">CDSE STAC API（按波段下载，需翻墙）</option>
        <option value="earth_search">Earth Search STAC API（无需认证，需翻墙）</option>
        <option value="custom">自定义 STAC API</option>
      </select>
    </div>

    <div class="field">
      <label for="satellite">卫星数据类型</label>
      <select id="satellite" name="satellite">
        <option value="sentinel-2-l2a">Sentinel-2 L2A（多光谱，云量过滤 + RGB）</option>
        <option value="sentinel-1-grd">Sentinel-1 GRD（SAR 雷达，VV/VH 极化）</option>
        <option value="sentinel-1-slc">Sentinel-1 SLC（SAR 雷达，VV/VH 极化）</option>
      </select>
    </div>

    <div id="cdse-box" class="hidden">
      <div class="panel cdse">
        <h3>CDSE 账号设置</h3>
        <div class="steps">
          <p><strong>第 1 步：</strong>前往 <a href="https://dataspace.copernicus.eu/" target="_blank">dataspace.copernicus.eu</a> 注册账号，点击右上角用户图标 → REGISTER，填写信息后查收验证邮件完成验证。</p>
          <p><strong>第 2 步：</strong>在下方填写 CDSE 登录邮箱和密码。</p>
        </div>

        <div class="field">
          <label>邮箱（用户名）</label>
          <input type="text" name="cdse_username" placeholder="{{if .HasExistingAuth}}留空保持不变（{{.ExistingUsername}}）{{else}}your@email.com{{end}}">
        </div>
        <div class="field">
          <label>密码</label>
          <input type="password" name="cdse_password" placeholder="{{if .HasExistingAuth}}留空保持不变{{else}}CDSE 登录密码{{end}}">
        </div>
        {{if .HasExistingAuth}}<p class="hint">已保存凭据：{{.ExistingUsername}}（两个输入框留空回车即可保持不变，只切换数据源）。</p>{{else}}<p class="hint">使用 CDSE 登录邮箱和密码，密码保存在本地。</p>{{end}}
      </div>
    </div>

    <div id="custom-box" class="field custom hidden">
      <div class="field">
        <label>STAC API 地址</label>
        <input type="text" name="stac_url" placeholder="https://example.com/stac">
      </div>
      <div class="field">
        <label>Collection 名称</label>
        <input type="text" name="collection" placeholder="SENTINEL-2">
      </div>
    </div>

    <button type="submit">保存并继续</button>
  </form>
</div>
<script>
function onSourceChange() {
  const v = document.getElementById('source').value;
  document.getElementById('cdse-box').classList.toggle('hidden', v !== 'cdse' && v !== 'cdse_odata');
  document.getElementById('custom-box').classList.toggle('hidden', v !== 'custom');
}
onSourceChange();
</script>
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
	HasExistingAuth  bool
	ExistingUsername string
}

var setupTmpl = template.Must(template.New("setup").Parse(setupHTML))

func runSetupWizard() (*Settings, error) {
	existing, _ := loadSettings()
	saved := hasSavedAuth(existing)

	resolveAuth := func(r *http.Request) (*AuthConfig, error) {
		user := strings.TrimSpace(r.FormValue("cdse_username"))
		pass := strings.TrimSpace(r.FormValue("cdse_password"))
		if user == "" && pass == "" {
			if saved {
				return &AuthConfig{Username: existing.Auth.Username, Password: existing.Auth.Password}, nil
			}
			return nil, fmt.Errorf("用户名和密码不能为空")
		}
		if user == "" || pass == "" {
			return nil, fmt.Errorf("用户名和密码必须同时提供")
		}
		return &AuthConfig{Username: user, Password: pass}, nil
	}

	done := make(chan *Settings, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Bad request", http.StatusBadRequest)
				return
			}

			source := r.FormValue("source")
			sat := r.FormValue("satellite")
			if sat == "" {
				sat = string(SatS2L2A)
			}
			sc := satelliteConfigs[SatelliteType(sat)]
			settings := &Settings{Source: source, Satellite: sat}

			switch source {
			case "earth_search":
				settings.STACURL = EarthSearchURL
				settings.Collection = sc.Collection
			case "cdse":
				settings.STACURL = "https://stac.dataspace.copernicus.eu/v1"
				settings.Collection = sc.CDSECollection
				auth, err := resolveAuth(r)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				settings.Auth = auth
			case "cdse_odata":
				auth, err := resolveAuth(r)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				settings.Auth = auth
			case "custom":
				settings.STACURL = strings.TrimSpace(r.FormValue("stac_url"))
				settings.Collection = strings.TrimSpace(r.FormValue("collection"))
				if settings.Collection != "" {
					settings.Satellite = string(ParseSatelliteType(settings.Collection))
				}
			default:
				http.Error(w, "Invalid source", http.StatusBadRequest)
				return
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
		data := setupPageData{HasExistingAuth: saved}
		if saved {
			data.ExistingUsername = existing.Auth.Username
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
