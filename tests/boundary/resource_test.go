//go:build linux

package boundary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-runtime-php/internal/phpop"
)

// lowFDHelperEnv 标记本进程是被父用例用 ulimit -n 重入的资源耗尽 helper。
const lowFDHelperEnv = "KORE_BOUNDARY_LOW_FD_HELPER"

// TestResourceExhaustionCommandOutputFlood 资源耗尽（真实注入）：fake php 是一个真实子进程，
// 在合法首行后连续写 2 × 64 MiB 输出。插件的 MaxCommandOutputBytes 上限必须把捕获内存
// 压在 1 MiB/命令，否则 plugin 进程会被外部命令刷爆内存（插件硬预算 ≤24 MiB）。
// 断言：版本仍解析成功、耗时可控、本进程在此期间总分配 < 32 MiB。
func TestResourceExhaustionCommandOutputFlood(t *testing.T) {
	fakeDir := t.TempDir()
	writeFakePHP(t, fakeDir, 64)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	started := time.Now()
	raw, perr := phpop.Call("runtime-php.versions", json.RawMessage(`{}`), "", nil)
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)

	if perr != nil {
		t.Fatalf("输出洪泛下 versions 失败: code=%s message=%s", perr.Code, perr.Message)
	}
	if elapsed > 20*time.Second {
		t.Fatalf("输出洪泛下 versions 耗时 %s，超过 20s 上限", elapsed)
	}
	const allocLimit = 32 << 20
	if delta := after.TotalAlloc - before.TotalAlloc; delta > allocLimit {
		t.Fatalf("真实注入 2×64MiB 子进程输出时本进程分配 %d MiB，超过上限 %d MiB"+
			"（MaxCommandOutputBytes 限流失效）", delta>>20, allocLimit>>20)
	}

	var result phpop.VersionsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("输出洪泛下结果不是合法 JSON: %v（%s）", err, raw)
	}
	found := false
	for _, item := range result.Versions {
		if item.Version == "8.2.0" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("输出洪泛下未能解析 fake php 8.2.0: %s", raw)
	}
}

// TestResourceExhaustionLowFileDescriptors 资源耗尽（真实注入）：用 bash ulimit -n 8 重跑本测试
// 二进制，在真实 fd 枯竭下并发调用 versions。插件必须把 exec 失败当候选失败跳过，
// 按降级契约返回合法 JSON（不 panic、不挂起、不把 fd 错误放大成协议错误）。
func TestResourceExhaustionLowFileDescriptors(t *testing.T) {
	if os.Getenv(lowFDHelperEnv) != "" {
		t.Skip("helper 只由父用例启动")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("定位测试二进制失败: %v", err)
	}
	fakeDir := t.TempDir()
	writeFakePHP(t, fakeDir, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c",
		`ulimit -n 8; exec "$0" -test.run '^TestResourceLowFDHelper$' -test.v`, exe)
	cmd.Env = append(os.Environ(),
		lowFDHelperEnv+"=1",
		"PATH="+fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("ulimit -n 8 下 helper 未优雅退出: %v\n%s", runErr, out)
	}
	if !strings.Contains(string(out), "BOUNDARY-LOW-FD-OK") {
		t.Fatalf("ulimit -n 8 下缺少降级完成标记:\n%s", out)
	}
}

// TestResourceLowFDHelper 只在 RLIMIT_NOFILE=8 的子进程里执行：断言真实 fd 枯竭下
// 并发 versions 调用全部按契约返回（perr=nil + 合法 JSON 版本列表）。
func TestResourceLowFDHelper(t *testing.T) {
	if os.Getenv(lowFDHelperEnv) != "1" {
		t.Skip("父用例通过 ulimit -n 重入执行")
	}
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatalf("读取 RLIMIT_NOFILE 失败: %v", err)
	}
	if limit.Cur > 8 {
		t.Fatalf("RLIMIT_NOFILE=%d，期望 ≤8（ulimit -n 未生效）", limit.Cur)
	}
	const workers = 32
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, perr := phpop.Call("runtime-php.versions", json.RawMessage(`{}`), "", nil)
			if perr != nil {
				errCh <- fmt.Errorf("fd 枯竭下 versions 未按降级契约返回: code=%s message=%s",
					perr.Code, perr.Message)
				return
			}
			var result phpop.VersionsResult
			if err := json.Unmarshal(raw, &result); err != nil {
				errCh <- fmt.Errorf("fd 枯竭下结果不是合法 JSON: %v（%s）", err, raw)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	fmt.Println("BOUNDARY-LOW-FD-OK")
}

// TestResourceExhaustionStderrENOSPC 资源耗尽（真实注入）：把插件进程的 stderr 接到 /dev/full，
// 日志写入必然返回 ENOSPC。插件唯一的输出面失效时仍必须按退出码契约退出 1，
// 不允许 panic、死循环或在 ENOSPC 上卡住。
func TestResourceExhaustionStderrENOSPC(t *testing.T) {
	binary := pluginBinary(t)
	devFull, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("/dev/full 不可用: %v", err)
	}
	defer func() { _ = devFull.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	// 缺 KORE_PLUGIN_SOCK 触发启动失败日志，stderr 正是被写满的输出面。
	cmd.Env = []string{"KORE_LOG_LEVEL=error", "PATH=/nonexistent"}
	cmd.Stderr = devFull
	runErr := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("stderr ENOSPC 时应退出 1，实际 err=%v", runErr)
	}
	if ctx.Err() != nil {
		t.Fatalf("stderr ENOSPC 下进程挂起超过 30s")
	}
}

// TestResourceExhaustionScanEntryCap 资源耗尽（真实注入）：造 250 个真实项目目录，
// 触发 MaxScanEntries=200 的扫描条目上限。断言扫描被截断、给出明确告警，
// 不把 250 个目录全量扫完（防目录轰炸拖垮插件）。
func TestResourceExhaustionScanEntryCap(t *testing.T) {
	state := t.TempDir()
	const created = 250
	for i := 0; i < created; i++ {
		name := fmt.Sprintf("app-%03d", i)
		writeProject(t, filepath.Join(state, name), "acme/"+name)
	}
	result := projectsViaCall(t, state, state)
	if result.Count == 0 {
		t.Fatal("条目上限触发前应至少收录部分项目")
	}
	if result.Count >= created {
		t.Fatalf("扫描条目上限未生效: 收录 %d 个项目（造了 %d 个）", result.Count, created)
	}
	joined := strings.Join(result.Warnings, " | ")
	if !strings.Contains(joined, "达到上限") {
		t.Fatalf("缺少扫描条目上限告警: %v", result.Warnings)
	}
}
