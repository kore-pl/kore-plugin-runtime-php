// Package e2e 在本机对 runtime-php 插件做真实端到端验证：
// 真实 php -v / -m / -i / --ini 探测、真实项目扫描与健康检查，以及"未安装 PHP"的降级路径。
package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-runtime-php/internal/phpop"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// versionPattern 是归一化后的 PHP 版本号格式。
var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// call 调用一个方法并打印真实 JSON（验收要求贴真实输出）。
func call(t *testing.T, method, params, stateRoot string) json.RawMessage {
	t.Helper()
	raw, perr := phpop.Call(method, json.RawMessage(params), stateRoot, nil)
	if perr != nil {
		t.Fatalf("%s 失败: code=%s message=%s", method, perr.Code, perr.Message)
	}
	t.Logf("%s => %s", method, raw)
	return raw
}

// requirePHP 返回本机 PATH 中的 php 路径；没有则跳过需要真实 PHP 的用例。
func requirePHP(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("php")
	if err != nil {
		t.Skip("本机 PATH 中没有 php，真实探测用例跳过（降级路径见 TestVersionsDegraded）")
	}
	return path
}

// TestVersionsReal 真实检测：版本格式、路径存在、source 合法、sapi=cli、
// extensions_count 与 php -m 一致、is_default 唯一且等于 which php。
func TestVersionsReal(t *testing.T) {
	phpPath := requirePHP(t)
	raw := call(t, "runtime-php.versions", `{}`, "")
	var result phpop.VersionsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}
	if result.Count == 0 || len(result.Versions) == 0 {
		t.Fatalf("本机存在 php（%s），但检测结果为空: %s", phpPath, raw)
	}
	if result.Count != len(result.Versions) {
		t.Fatalf("count=%d 与 versions 长度 %d 不一致", result.Count, len(result.Versions))
	}
	if !versionPattern.MatchString(result.DefaultVersion) {
		t.Fatalf("default_version 格式非法: %q", result.DefaultVersion)
	}
	defaults := 0
	for _, item := range result.Versions {
		if !versionPattern.MatchString(item.Version) {
			t.Fatalf("版本格式非法: %q", item.Version)
		}
		if _, err := os.Stat(item.Path); err != nil {
			t.Fatalf("版本路径不存在: %q (%v)", item.Path, err)
		}
		switch item.Source {
		case phpop.SourcePath, phpop.SourceKnownPath, phpop.SourcePhpenv:
		default:
			t.Fatalf("source 非法: %q", item.Source)
		}
		if item.SAPI != "cli" {
			t.Fatalf("sapi 必须为 cli: %+v", item)
		}
		if item.ExtensionsCount <= 0 {
			t.Fatalf("extensions_count 应大于 0: %+v", item)
		}
		if item.IsDefault {
			defaults++
			if item.Version != result.DefaultVersion {
				t.Fatalf("默认条目版本 %q 与 default_version %q 不一致", item.Version, result.DefaultVersion)
			}
			if err := sameFile(phpPath, item.Path); err != nil {
				t.Fatalf("is_default 条目与 which php 不一致: %v", err)
			}
			if got := realExtensionCount(t, item.Path); got != item.ExtensionsCount {
				t.Fatalf("extensions_count=%d 与 php -m 实测 %d 不一致", item.ExtensionsCount, got)
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("is_default=true 的条目应恰好 1 个，实际 %d", defaults)
	}
	t.Logf("本机默认 PHP %s（%s），共检测到 %d 个安装", result.DefaultVersion, phpPath, result.Count)
}

// realExtensionCount 用 `php -m` 独立统计模块数（与插件内部解析互不依赖）。
func realExtensionCount(t *testing.T, phpPath string) int {
	t.Helper()
	out, err := exec.Command(phpPath, "-m").Output()
	if err != nil {
		t.Fatalf("执行 %s -m 失败: %v", phpPath, err)
	}
	count := 0
	inModules := false
	sawHeader := false
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			sawHeader = true
			inModules = trimmed == "[PHP Modules]"
			continue
		}
		if sawHeader && !inModules {
			continue
		}
		count++
	}
	return count
}

// TestInfoReal 详情：真实版本、扩展数量、ini 路径与 memory_limit。
func TestInfoReal(t *testing.T) {
	requirePHP(t)
	raw := call(t, "runtime-php.info", `{}`, "")
	var result phpop.InfoResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}
	if !versionPattern.MatchString(result.Version) || result.PHPPath == "" {
		t.Fatalf("版本/路径为空: %+v", result)
	}
	if _, err := os.Stat(result.PHPPath); err != nil {
		t.Fatalf("php_path 不存在: %v", err)
	}
	if len(result.Extensions) == 0 {
		t.Fatalf("扩展列表为空（php -m 应成功）: %+v", result)
	}
	if result.IniSettings.MemoryLimit == "" {
		t.Fatalf("memory_limit 为空（php -i 应成功）: %+v", result.IniSettings)
	}
	if result.IniPath != "" {
		if _, err := os.Stat(result.IniPath); err != nil {
			t.Fatalf("ini_path 不存在: %q (%v)", result.IniPath, err)
		}
	} else {
		t.Logf("本机未加载 php.ini（ini_path 为空），ini_settings 取编译默认值")
	}
	t.Logf("真实详情：php=%s 扩展数=%d ini=%q memory_limit=%s upload_max_filesize=%s post_max_size=%s max_execution_time=%s",
		result.Version, len(result.Extensions), result.IniPath,
		result.IniSettings.MemoryLimit, result.IniSettings.UploadMaxFilesize,
		result.IniSettings.PostMaxSize, result.IniSettings.MaxExecutionTime)
}

// TestInfoMajorMinor 用真实版本的主次前缀请求详情，必须选中同一分支。
func TestInfoMajorMinor(t *testing.T) {
	requirePHP(t)
	versionsRaw := call(t, "runtime-php.versions", `{}`, "")
	var versions phpop.VersionsResult
	if err := json.Unmarshal(versionsRaw, &versions); err != nil {
		t.Fatalf("解析 versions 失败: %v", err)
	}
	parts := strings.Split(versions.DefaultVersion, ".")
	if len(parts) != 3 {
		t.Fatalf("默认版本非法: %q", versions.DefaultVersion)
	}
	branch := parts[0] + "." + parts[1]
	raw := call(t, "runtime-php.info", `{"version":"`+branch+`"}`, "")
	var info phpop.InfoResult
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("解析 info 失败: %v", err)
	}
	if !strings.HasPrefix(info.Version, branch+".") {
		t.Fatalf("请求 %s 得到版本 %q", branch, info.Version)
	}
}

// TestInfoVersionNotInstalled 指定未安装版本按 E_INVALID 返回，不误报。
func TestInfoVersionNotInstalled(t *testing.T) {
	requirePHP(t)
	_, perr := phpop.Call("runtime-php.info", json.RawMessage(`{"version":"0.0.1"}`), "", nil)
	if perr == nil || perr.Code != pluginapi.E_INVALID {
		t.Fatalf("错误=%v，期望 E_INVALID", perr)
	}
}

// testRoot 是验收指定的真实项目目录。
const testRoot = "/tmp/kore-php-test"

// prepareProjects 造 2 个项目：1 个正常 composer.json、1 个坏 JSON。
func prepareProjects(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(testRoot); err != nil {
		t.Fatalf("清理 %s 失败: %v", testRoot, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(testRoot) })
	files := map[string]string{
		filepath.Join(testRoot, "good-app", "composer.json"): `{
  "name": "acme/good-app",
  "require": {
    "php": "^8.1",
    "ext-mbstring": "*",
    "laravel/framework": "^10.0",
    "monolog/monolog": "^3.0"
  }
}`,
		filepath.Join(testRoot, "good-app", "composer.lock"): `{}`,
		filepath.Join(testRoot, "bad-json", "composer.json"): `{ "name": "broken", `,
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("创建目录失败: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("写入文件失败: %v", err)
		}
	}
}

// TestProjectsReal 真实扫描 /tmp/kore-php-test：1 个正常项目 + 坏 JSON 的 warning。
func TestProjectsReal(t *testing.T) {
	prepareProjects(t)
	raw := call(t, "runtime-php.projects", `{"root":"`+testRoot+`"}`, testRoot)
	var result phpop.ProjectsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}
	if result.Count != 1 || len(result.Projects) != 1 {
		t.Fatalf("期望 1 个项目，实际 count=%d projects=%+v warnings=%v", result.Count, result.Projects, result.Warnings)
	}
	project := result.Projects[0]
	if project.Name != "acme/good-app" || project.PHPRequirement != "^8.1" {
		t.Fatalf("项目字段错误: %+v", project)
	}
	if project.DependenciesCount != 2 || !project.HasLock {
		t.Fatalf("依赖数/锁文件错误: %+v", project)
	}
	if project.Framework != "laravel" {
		t.Fatalf("framework=%q，期望 laravel", project.Framework)
	}
	if !strings.HasSuffix(project.Path, "good-app") {
		t.Fatalf("项目路径错误: %q", project.Path)
	}
	if len(result.Warnings) == 0 || !strings.Contains(strings.Join(result.Warnings, " | "), "bad-json") {
		t.Fatalf("坏 JSON 未产生 warnings: %v", result.Warnings)
	}
	t.Logf("扫描到 %d 个项目；warnings=%v", result.Count, result.Warnings)
}

// TestDoctorReal 健康检查返回真实检查项；扩展状态必须与真实 php -m 一致。
func TestDoctorReal(t *testing.T) {
	phpPath := requirePHP(t)
	prepareProjects(t)
	raw := call(t, "runtime-php.doctor", `{"root":"`+testRoot+`"}`, testRoot)
	var result phpop.DoctorResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}
	if len(result.Checks) == 0 {
		t.Fatal("checks 为空")
	}
	statuses := map[string]string{}
	fails := 0
	for _, check := range result.Checks {
		if check.Name == "" || check.Detail == "" {
			t.Fatalf("检查项字段不完整: %+v", check)
		}
		switch check.Status {
		case phpop.StatusPass, phpop.StatusWarn:
		case phpop.StatusFail:
			fails++
		default:
			t.Fatalf("status 非法: %+v", check)
		}
		statuses[check.Name] = check.Status
		t.Logf("check %-26s %-4s %s", check.Name, check.Status, check.Detail)
	}
	required := []string{"php", "version_supported", "memory_limit", "ini_loaded",
		"ext_mbstring", "ext_json", "ext_pdo", "ext_openssl", "ext_curl", "ext_fileinfo", "project_root"}
	for _, name := range required {
		if _, ok := statuses[name]; !ok {
			t.Fatalf("缺少检查项 %q: %+v", name, statuses)
		}
	}
	if result.OK != (fails == 0) {
		t.Fatalf("ok=%v 与 fail 数 %d 不一致", result.OK, fails)
	}
	if statuses["php"] != phpop.StatusPass {
		t.Fatalf("本机有 php，但 php 检查=%q", statuses["php"])
	}
	if statuses["project_root"] != phpop.StatusPass {
		t.Fatalf("项目目录应有 1 个项目: %q", statuses["project_root"])
	}
	// 独立用真实 php -m 交叉验证扩展状态（不信插件自己的解析）。
	modules := realModules(t, phpPath)
	for _, name := range []string{"mbstring", "json", "pdo", "openssl", "curl", "fileinfo"} {
		status := statuses["ext_"+name]
		present := modules[name]
		if present && status != phpop.StatusPass {
			t.Fatalf("扩展 %s 实际已加载，但检查状态=%q", name, status)
		}
		if !present && (status == phpop.StatusPass) {
			t.Fatalf("扩展 %s 实际未加载，但检查状态=%q", name, status)
		}
	}
}

// realModules 用真实 `php -m` 得到模块集合（独立于插件内部实现）。
func realModules(t *testing.T, phpPath string) map[string]bool {
	t.Helper()
	out, err := exec.Command(phpPath, "-m").Output()
	if err != nil {
		t.Fatalf("执行 %s -m 失败: %v", phpPath, err)
	}
	modules := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || (strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			continue
		}
		modules[strings.ToLower(trimmed)] = true
	}
	return modules
}

// TestVersionsDegraded 无 PHP 时 versions 不报错：空数组 + count=0 + 固定 hint。
// 通过"空执行面"走 Call 的降级分支，验证协议层形状（真实无 PHP 环境的检测降级见二进制用例）。
func TestVersionsDegraded(t *testing.T) {
	raw, perr := phpop.Call("runtime-php.versions", json.RawMessage(`{}`), "", emptyExecutor{})
	if perr != nil {
		t.Fatalf("无 PHP 时不应报错: %v", perr)
	}
	var result phpop.VersionsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}
	t.Logf("降级 JSON => %s", raw)
	if result.Count != 0 || len(result.Versions) != 0 {
		t.Fatalf("期望空版本列表: %+v", result)
	}
	if result.Hint != phpop.NoPHPHint {
		t.Fatalf("hint=%q，期望 %q", result.Hint, phpop.NoPHPHint)
	}
	if !strings.Contains(string(raw), `"versions":[]`) {
		t.Fatalf("versions 必须是空数组: %s", raw)
	}
}

// emptyExecutor 模拟"一个 PHP 都没有"的宿主执行面（仅用于协议层降级断言）。
type emptyExecutor struct{}

func (emptyExecutor) Versions() (phpop.VersionsResult, error) {
	return phpop.VersionsResult{Versions: []phpop.PHPVersion{}}, nil
}

func (emptyExecutor) Info(string) (phpop.InfoResult, error) {
	return phpop.InfoResult{}, phpop.ErrPHPNotInstalled
}

func (emptyExecutor) Projects(string) (phpop.ProjectsResult, error) {
	return phpop.ProjectsResult{}, nil
}

func (emptyExecutor) Doctor(string) (phpop.DoctorResult, error) {
	return phpop.DoctorResult{Checks: []phpop.Check{{Name: "php", Status: phpop.StatusFail, Detail: phpop.NoPHPHint}}}, nil
}

// sameFile 判定两个路径是否指向同一文件（跟随符号链接）。
func sameFile(left, right string) error {
	leftReal, err := filepath.EvalSymlinks(left)
	if err != nil {
		return err
	}
	rightReal, err := filepath.EvalSymlinks(right)
	if err != nil {
		return err
	}
	if leftReal != rightReal {
		return &pathMismatchError{left: leftReal, right: rightReal}
	}
	return nil
}

type pathMismatchError struct{ left, right string }

func (e *pathMismatchError) Error() string { return e.left + " != " + e.right }
