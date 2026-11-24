//go:build linux

package boundary

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-runtime-php/internal/phpop"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// 权限不足用例全部做真实 chmod 000 注入（非 root 下 EACCES 生效）：
// 目录不可搜索 → root 校验失败（E_INVALID）；项目目录不可读 → 明确降级告警；
// composer.json 不可读 → 跳过 + 告警；UDS 所在目录不可搜索 → 插件进程按退出码 1 失败。
// root 身份会绕过 DAC 权限位，用例显式跳过，避免"root 下永远通过"的假绿。
func requireNonRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root 绕过 DAC 权限位，权限用例在 root 身份下不成立")
	}
}

// TestPermissionDeniedRootPath 目录不可搜索时 root 校验必须明确失败（E_INVALID），
// 而不是把"读不到"当成"空项目列表"静默放过。
func TestPermissionDeniedRootPath(t *testing.T) {
	requireNonRoot(t)
	state := t.TempDir()
	locked := filepath.Join(state, "locked")
	writeProject(t, filepath.Join(locked, "app"), "acme/locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatalf("chmod 000 %s 失败: %v", locked, err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	root := filepath.Join(locked, "app")
	_, perr := phpop.Call("runtime-php.projects",
		json.RawMessage(`{"root":`+jsonString(t, root)+`}`), state, nil)
	if perr == nil {
		t.Fatal("目录不可搜索时 projects 应返回错误，实际成功")
	}
	if perr.Code != pluginapi.E_INVALID {
		t.Fatalf("错误码=%q，期望 %q（message=%q）", perr.Code, pluginapi.E_INVALID, perr.Message)
	}
	lower := strings.ToLower(perr.Message)
	if !strings.Contains(lower, "root") {
		t.Fatalf("错误信息未指明 root 字段: %q", perr.Message)
	}
	if !strings.Contains(lower, "permission denied") && !strings.Contains(perr.Message, "不可访问") {
		t.Fatalf("错误信息未体现权限拒绝: %q", perr.Message)
	}
}

// TestPermissionDeniedDirectoryScan 项目目录不可读时扫描不中断：跳过该目录并写明确告警，
// 其余可读项目照常返回（只读扫描的降级契约）。
func TestPermissionDeniedDirectoryScan(t *testing.T) {
	requireNonRoot(t)
	state := t.TempDir()
	writeProject(t, filepath.Join(state, "readable"), "acme/readable")
	unreadable := filepath.Join(state, "unreadable")
	writeProject(t, unreadable, "acme/unreadable")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatalf("chmod 000 %s 失败: %v", unreadable, err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o755) })

	result := projectsViaCall(t, state, state)
	if result.Count != 1 || len(result.Projects) != 1 || result.Projects[0].Name != "acme/readable" {
		t.Fatalf("不可读目录应被跳过、只返回可读项目: count=%d projects=%+v", result.Count, result.Projects)
	}
	joined := strings.Join(result.Warnings, " | ")
	if !strings.Contains(joined, "不可读") {
		t.Fatalf("缺少「目录不可读」告警: %v", result.Warnings)
	}
	for _, project := range result.Projects {
		if strings.Contains(project.Path, "unreadable") {
			t.Fatalf("不可读目录下的项目被错误收录: %+v", project)
		}
	}
}

// TestPermissionDeniedComposerFile composer.json 不可读时跳过该项目并告警，
// 不能把权限错误吞成"没有项目"。
func TestPermissionDeniedComposerFile(t *testing.T) {
	requireNonRoot(t)
	state := t.TempDir()
	composer := filepath.Join(state, "app", "composer.json")
	writeFile(t, composer, `{"name":"acme/app","require":{"php":"^8.1"}}`, 0o644)
	if err := os.Chmod(composer, 0); err != nil {
		t.Fatalf("chmod 000 %s 失败: %v", composer, err)
	}
	t.Cleanup(func() { _ = os.Chmod(composer, 0o644) })

	result := projectsViaCall(t, state, state)
	if result.Count != 0 || len(result.Projects) != 0 {
		t.Fatalf("不可读 composer.json 不应产生项目: %+v", result.Projects)
	}
	joined := strings.Join(result.Warnings, " | ")
	if !strings.Contains(joined, "读取 composer.json 失败") {
		t.Fatalf("缺少「读取 composer.json 失败」告警: %v", result.Warnings)
	}
}

// TestPermissionDeniedSocket UDS 所在目录不可搜索时，插件进程必须按退出码契约失败（1），
// 且错误信息指向连接主壳 socket；不允许 panic 或静默挂起。
func TestPermissionDeniedSocket(t *testing.T) {
	requireNonRoot(t)
	binary := pluginBinary(t)
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatalf("创建 socket 目录失败: %v", err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatalf("chmod 000 %s 失败: %v", locked, err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	cmd := exec.Command(binary)
	cmd.Env = []string{
		"KORE_PLUGIN_SOCK=" + filepath.Join(locked, "plugin.sock"),
		"KORE_PLUGIN_ID=runtime-php",
		"KORE_PLUGIN_TOKEN=boundary-test",
		"KORE_LOG_LEVEL=error",
		"PATH=/nonexistent",
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("socket 目录无权限时应退出 1，实际 err=%v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "连接主壳 socket") {
		t.Fatalf("stderr 未指明连接主壳 socket 失败: %s", stderr.String())
	}
}
