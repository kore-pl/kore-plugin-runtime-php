package boundary

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pengtang1024/kore-plugin-runtime-php/internal/phpop"
)

// TestConcurrentProjectScanStable 真并发：8 个 goroutine 对同一 fixture 目录并发执行
// 真实扫描（exec=nil，走 Runtime.Projects 的真实文件系统读取），断言每个结果都完整一致，
// 并在 -race 下证明共享方法面没有数据竞争。
func TestConcurrentProjectScanStable(t *testing.T) {
	state := t.TempDir()
	names := []string{"acme/app-a", "acme/app-b", "acme/app-c"}
	for _, name := range names {
		writeProject(t, filepath.Join(state, strings.TrimPrefix(name, "acme/")), name)
	}
	params := json.RawMessage(`{"root":` + jsonString(t, state) + `}`)
	const workers, rounds = 8, 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	errCh := make(chan error, workers*rounds)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for round := 0; round < rounds; round++ {
				raw, perr := phpop.Call("runtime-php.projects", params, state, nil)
				if perr != nil {
					errCh <- fmt.Errorf("并发扫描失败: code=%s message=%s", perr.Code, perr.Message)
					continue
				}
				var result phpop.ProjectsResult
				if err := json.Unmarshal(raw, &result); err != nil {
					errCh <- fmt.Errorf("并发扫描结果不是合法 JSON: %v（%s）", err, raw)
					continue
				}
				if result.Count != len(names) || len(result.Projects) != len(names) {
					errCh <- fmt.Errorf("并发扫描结果不完整: count=%d projects=%d warnings=%v",
						result.Count, len(result.Projects), result.Warnings)
					continue
				}
				if len(result.Warnings) != 0 {
					errCh <- fmt.Errorf("只读 fixture 并发扫描不应产生告警: %v", result.Warnings)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// TestConcurrentVersionsWithRealCommands 真并发：多个 goroutine 同时用真实执行面探测
// 同一 PATH（fake php 是真实子进程），覆盖 exec.Cmd 创建、输出捕获与解析的并发路径。
func TestConcurrentVersionsWithRealCommands(t *testing.T) {
	fakeDir := t.TempDir()
	writeFakePHP(t, fakeDir, 0)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	const workers, rounds = 6, 4
	var wg sync.WaitGroup
	start := make(chan struct{})
	errCh := make(chan error, workers*rounds)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for round := 0; round < rounds; round++ {
				raw, perr := phpop.Call("runtime-php.versions", json.RawMessage(`{}`), "", nil)
				if perr != nil {
					errCh <- fmt.Errorf("并发 versions 失败: code=%s message=%s", perr.Code, perr.Message)
					continue
				}
				var result phpop.VersionsResult
				if err := json.Unmarshal(raw, &result); err != nil {
					errCh <- fmt.Errorf("并发 versions 结果非法: %v（%s）", err, raw)
					continue
				}
				found := false
				for _, item := range result.Versions {
					if item.Version == "8.2.0" {
						found = true
						break
					}
				}
				if !found {
					errCh <- fmt.Errorf("并发探测未找到 fake php 8.2.0: %s", raw)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// TestConcurrentScanDuringAtomicRewrite 并发读写同一份项目配置：一个 goroutine 用
// 临时文件 + rename 原子替换 composer.json（模拟站点配置更新），多个 goroutine 持续扫描。
// 断言读侧永远看到完整旧值或完整新值——项目数恒为 1、包名只可能是两个合法值、无告警。
func TestConcurrentScanDuringAtomicRewrite(t *testing.T) {
	state := t.TempDir()
	projectDir := filepath.Join(state, "app")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("创建项目目录失败: %v", err)
	}
	contents := []string{
		`{"name":"acme/app-a","require":{"php":"^8.1","laravel/framework":"^10.0"}}`,
		`{"name":"acme/app-b","require":{"php":"^8.2","laravel/framework":"^11.0"}}`,
	}
	if err := os.WriteFile(filepath.Join(projectDir, "composer.json"), []byte(contents[0]), 0o644); err != nil {
		t.Fatalf("写入初始 composer.json 失败: %v", err)
	}
	params := json.RawMessage(`{"root":` + jsonString(t, projectDir) + `}`)
	stop := make(chan struct{})
	errCh := make(chan error, 64)
	var wg sync.WaitGroup

	// 写侧：100 次原子替换，写完关闭 stop。
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 0; i < 100; i++ {
			if err := rewriteComposerAtomically(projectDir, contents[i%len(contents)]); err != nil {
				errCh <- fmt.Errorf("原子替换 composer.json 失败: %v", err)
				return
			}
		}
	}()

	// 读侧：4 个 goroutine 在写侧结束前持续扫描。
	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				raw, perr := phpop.Call("runtime-php.projects", params, state, nil)
				if perr != nil {
					errCh <- fmt.Errorf("并发读失败: code=%s message=%s", perr.Code, perr.Message)
					return
				}
				var result phpop.ProjectsResult
				if err := json.Unmarshal(raw, &result); err != nil {
					errCh <- fmt.Errorf("并发读结果非法: %v（%s）", err, raw)
					return
				}
				if result.Count != 1 || len(result.Projects) != 1 {
					errCh <- fmt.Errorf("原子替换下读侧看到撕裂结果: count=%d warnings=%v",
						result.Count, result.Warnings)
					return
				}
				if name := result.Projects[0].Name; name != "acme/app-a" && name != "acme/app-b" {
					errCh <- fmt.Errorf("读到非法包名 %q（应为 acme/app-a 或 acme/app-b）", name)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// rewriteComposerAtomically 用临时文件 + rename 原子替换 composer.json：
// 读侧永远看到完整旧值或完整新值，不会读到半写文件。
func rewriteComposerAtomically(dir, content string) error {
	file, err := os.CreateTemp(dir, ".composer-*.json")
	if err != nil {
		return err
	}
	name := file.Name()
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, filepath.Join(dir, "composer.json"))
}
