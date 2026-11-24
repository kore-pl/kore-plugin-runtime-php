// Package boundary 是 runtime-php 的边界用例集（G-20 production 判据①）。
//
// 四类覆盖，全部真实执行，不用进程内旗标：
//   - 异常输入：非法 JSON 载荷 / 未知字段 / 非法版本 / 越权 root / 未声明方法，断言明确协议错误码；
//   - 并发：同一 fixture 上的并发项目扫描、配置原子替换下的并发读、并发版本探测；
//   - 权限不足：chmod 000 的目录 / composer.json / UDS 所在目录，断言 E_INVALID 或明确的降级告警；
//   - 资源耗尽：真实 64 MiB 子进程输出洪泛（MaxCommandOutputBytes 内存上限）、
//     真实 RLIMIT_NOFILE（ulimit -n 8）下的优雅降级、/dev/full ENOSPC 下的退出码契约、
//     真实 250 目录触发的扫描条目上限。
//
// 事实边界（2026-11-24 核实）：本插件只读探测 PHP CLI，不管理 FPM 进程池、
// 不切 /etc/alternatives/php、不写站点配置（README「不做」节、types.go 的 SAPI 注释），
// 因此并发/权限用例不伪造 FPM 与站点配置写入；生产代码零写路径，
// 所以 /dev/full 只作用于唯一的输出面（stderr 日志），tmpfs 写满对只读扫描没有注入目标，
// 不为了凑关键词伪造这类场景。
package boundary

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pengtang1024/kore-plugin-runtime-php/internal/phpop"
)

// 全包复用的真实插件二进制：权限（UDS 目录）与 ENOSPC 用例必须在进程边界验证。
var (
	pluginBuildOnce sync.Once
	pluginDir       string
	pluginBin       string
	pluginBuildErr  error
)

// TestMain 在包级用例结束后清理临时构建目录，不在工作区留临时产物。
func TestMain(m *testing.M) {
	code := m.Run()
	if pluginDir != "" {
		_ = os.RemoveAll(pluginDir)
	}
	os.Exit(code)
}

// pluginBinary 构建真实插件二进制：构建参数与 Makefile build 目标一致
// （CGO_ENABLED=0 + GOWORK=off + trimpath），避免测试产物与发布产物行为分叉。
func pluginBinary(t *testing.T) string {
	t.Helper()
	pluginBuildOnce.Do(func() {
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			pluginBuildErr = fmt.Errorf("解析仓库根目录: %w", err)
			return
		}
		pluginDir, err = os.MkdirTemp("", "kore-runtime-php-boundary-*")
		if err != nil {
			pluginBuildErr = fmt.Errorf("创建临时构建目录: %w", err)
			return
		}
		pluginBin = filepath.Join(pluginDir, "runtime-php")
		cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", pluginBin, ".")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
		if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
			pluginBuildErr = fmt.Errorf("构建插件二进制失败: %v\n%s", buildErr, out)
		}
	})
	if pluginBuildErr != nil {
		t.Fatal(pluginBuildErr)
	}
	return pluginBin
}

// jsonString 把路径编码为 JSON 字符串字面量。
func jsonString(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("编码 JSON 字符串失败: %v", err)
	}
	return string(data)
}

// writeFile 写一个 fixture 文件（自动建父目录，用于真实文件系统注入）。
func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录 %s 失败: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
}

// writeProject 在 dir 下造一个最小 composer.json。
func writeProject(t *testing.T, dir, name string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "composer.json"), `{
  "name": "`+name+`",
  "require": {"php": "^8.1", "laravel/framework": "^10.0"}
}`, 0o644)
}

// projectsViaCall 用真实执行面扫描 root（成功路径），失败即终止。
func projectsViaCall(t *testing.T, stateRoot, root string) phpop.ProjectsResult {
	t.Helper()
	raw, perr := phpop.Call("runtime-php.projects",
		json.RawMessage(`{"root":`+jsonString(t, root)+`}`), stateRoot, nil)
	if perr != nil {
		t.Fatalf("projects(root=%s) 失败: code=%s message=%s", root, perr.Code, perr.Message)
	}
	var result phpop.ProjectsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("解析 projects 结果失败: %v（%s）", err, raw)
	}
	return result
}

// writeFakePHP 造一个真实可执行 shell 脚本冒充 php。
// floodMiB > 0 时在合法首行后追加真实输出洪泛（dd 写 NUL 字节），用于资源耗尽注入。
func writeFakePHP(t *testing.T, dir string, floodMiB int) string {
	t.Helper()
	flood := ""
	if floodMiB > 0 {
		flood = fmt.Sprintf("; dd if=/dev/zero bs=1048576 count=%d 2>/dev/null", floodMiB)
	}
	script := `#!/bin/sh
case "$1" in
  -v) printf 'PHP 8.2.0 (cli) (NTS)\n'` + flood + `;;
  -m) printf '[PHP Modules]\nCore\njson\nmbstring\npdo\nopenssl\n\n[Zend Modules]\nZend OPcache\n'` + flood + `;;
  -i) printf 'memory_limit => 256M => 256M\nupload_max_filesize => 2M => 2M\npost_max_size => 8M => 8M\nmax_execution_time => 0 => 0\n';;
  --ini) printf 'Loaded Configuration File: (none)\n';;
esac
exit 0
`
	path := filepath.Join(dir, "php")
	writeFile(t, path, script, 0o755)
	return path
}
