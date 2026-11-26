package phpop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// extFake 在 stubExecutor 之上实现 ExtManager，记录调用参数并返回预设结果。
type extFake struct {
	stubExecutor
	installPrefix string
	installPkgs   []string
	installResult ExtInstallResult
	installErr    error
	installCalls  int

	listResult ExtListResult
	listErr    error
	listCalls  int

	removePrefix string
	removePkgs   []string
	removeResult ExtRemoveResult
	removeErr    error
	removeCalls  int
}

func (f *extFake) InstallPackages(prefix string, packages []string) (ExtInstallResult, error) {
	f.installCalls++
	f.installPrefix, f.installPkgs = prefix, packages
	return f.installResult, f.installErr
}

func (f *extFake) ListPackages(prefix string) (ExtListResult, error) {
	f.listCalls++
	return f.listResult, f.listErr
}

func (f *extFake) RemovePackages(prefix string, packages []string) (ExtRemoveResult, error) {
	f.removeCalls++
	f.removePrefix, f.removePkgs = prefix, packages
	return f.removeResult, f.removeErr
}

func mustCallExt[T any](t *testing.T, method, params, root string, exec Executor) T {
	t.Helper()
	raw, perr := Call(method, json.RawMessage(params), root, exec)
	if perr != nil {
		t.Fatalf("%s 失败: %+v", method, perr)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s 结果解析失败: %v（%s）", method, err, raw)
	}
	return out
}

// TestExtInstallPrefixIsolated 校验 install/list/remove 的 prefix 固定为 <state>/global，
// 与宿主 php 扩展目录 / apt 全局无关（不写系统）。
func TestExtInstallPrefixIsolated(t *testing.T) {
	root := t.TempDir()
	fake := &extFake{}
	want := filepath.Join(root, DefaultGlobalSubdir)
	result := mustCallExt[ExtInstallResult](t, "runtime-php.ext.install",
		`{"packages":["redis-6.0.0"]}`, root, fake)
	if result.Prefix != want || fake.installPrefix != want {
		t.Fatalf("隔离前缀不对: result=%s fake=%s want=%s", result.Prefix, fake.installPrefix, want)
	}
	if !result.NetworkUsed || len(result.Packages) != 1 || result.Packages[0] != "redis-6.0.0" {
		t.Fatalf("安装结果字段不对: %+v", result)
	}
	list := mustCallExt[ExtListResult](t, "runtime-php.ext.list", `{}`, root, fake)
	if list.Prefix != want {
		t.Fatalf("list 前缀不对: %s", list.Prefix)
	}
	_ = mustCallExt[ExtRemoveResult](t, "runtime-php.ext.remove",
		`{"packages":["redis"]}`, root, fake)
	if fake.removePrefix != want {
		t.Fatalf("remove 前缀不对: %s", fake.removePrefix)
	}
	if strings.HasSuffix(want, extSubdir) {
		t.Fatalf("前缀应是隔离根 <state>/global，而非扩展子目录字面路径: %s", want)
	}
}

// TestExtRejectsGlobalAndInjection 校验包名白名单挡 apt / -g / 注入 / 斜杠 / 空列表。
func TestExtRejectsGlobalAndInjection(t *testing.T) {
	root := t.TempDir()
	fake := &extFake{}
	bad := []string{
		"-g", "--global", "apt install php-redis", "redis; rm -rf /", "redis extra",
		"../evil", "/etc/php/8.3", "redis$(id)", "redis`whoami`", "",
	}
	for _, pkg := range bad {
		payload, _ := json.Marshal(map[string]any{"packages": []string{pkg}})
		_, perr := Call("runtime-php.ext.install", payload, root, fake)
		if perr == nil || perr.Code != pluginapi.E_INVALID {
			t.Fatalf("包名 %q 期望 E_INVALID，得到 %+v", pkg, perr)
		}
	}
	_, perr := Call("runtime-php.ext.install", json.RawMessage(`{}`), root, fake)
	if perr == nil || perr.Code != pluginapi.E_INVALID {
		t.Fatalf("空 packages 期望 E_INVALID，得到 %+v", perr)
	}
	if fake.installCalls != 0 {
		t.Fatalf("参数校验失败仍触达执行面: %d", fake.installCalls)
	}
	// 未知字段（如 prefix / global）也必须拒绝，防止调用方绕过固定前缀。
	for _, params := range []string{`{"packages":["redis"],"prefix":"/etc"}`, `{"packages":["redis"],"global":true}`} {
		_, perr = Call("runtime-php.ext.install", json.RawMessage(params), root, fake)
		if perr == nil || perr.Code != pluginapi.E_INVALID {
			t.Fatalf("未知字段参数 %s 期望 E_INVALID，得到 %+v", params, perr)
		}
	}
}

// TestExtPrefixRejectsEscape 校验状态目录含 .. / 相对路径 / 首尾空白时被拒。
func TestExtPrefixRejectsEscape(t *testing.T) {
	fake := &extFake{}
	for _, root := range []string{"/tmp/../etc", "relative/state", " /tmp/state", "/tmp/state ", ""} {
		_, perr := Call("runtime-php.ext.list", json.RawMessage(`{}`), root, fake)
		if perr == nil || perr.Code != pluginapi.E_INVALID {
			t.Fatalf("stateRoot=%q 期望 E_INVALID，得到 %+v", root, perr)
		}
	}
	// <state>/global 被软链指向状态目录之外时，真实层校验必须拒绝（软链逃逸）。
	state := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(state, DefaultGlobalSubdir)); err != nil {
		t.Skipf("本环境不支持符号链接: %v", err)
	}
	_, perr := Call("runtime-php.ext.list", json.RawMessage(`{}`), state, fake)
	if perr == nil || perr.Code != pluginapi.E_INVALID {
		t.Fatalf("global 软链逃逸期望 E_INVALID，得到 %+v", perr)
	}
}

// TestExtTruncateOutput 校验输出超限时保留首尾与标记。
func TestExtTruncateOutput(t *testing.T) {
	raw := strings.Repeat("a", MaxExtOutputBytes+100)
	truncated, ok := truncateExtOutput(raw)
	if !ok || !strings.Contains(truncated, "输出截断") || len(truncated) >= len(raw) {
		t.Fatalf("截断逻辑异常: ok=%v len=%d", ok, len(truncated))
	}
	if ok := extPackageSpecPattern.MatchString("--global"); ok {
		t.Fatal("--global 选项不应匹配包名白名单")
	}
}

// TestExtRealManagerArgv 校验真实执行面固定 argv：pecl + extension_dir 指向隔离前缀，
// 绝不出现 apt / -g / --global；uninstall 剥掉版本后缀。
func TestExtRealManagerArgv(t *testing.T) {
	var captured []string
	mgr := realExtManager{runner: func(timeout time.Duration, name string, args ...string) (string, error) {
		if name != "pecl" || timeout != ExtCommandTimeout {
			t.Fatalf("固定 argv 异常: name=%s timeout=%s", name, timeout)
		}
		captured = append([]string{}, args...)
		return "ok", nil
	}}
	prefix := filepath.Join(t.TempDir(), DefaultGlobalSubdir)
	if _, err := mgr.InstallPackages(prefix, []string{"redis", "memcached-3.1.5"}); err != nil {
		t.Fatalf("install 失败: %v", err)
	}
	joined := strings.Join(captured, " ")
	if strings.Contains(joined, "apt") || strings.Contains(joined, "-g") || strings.Contains(joined, "--global") {
		t.Fatalf("真实执行面出现了全局/apt 安装选项: %s", joined)
	}
	wantDir := "extension_dir=" + filepath.Join(prefix, extSubdir)
	if captured[0] != "install" || captured[1] != "-d" || captured[2] != wantDir {
		t.Fatalf("未使用固定隔离 extension_dir: %v", captured)
	}
	if captured[len(captured)-2] != "redis" || captured[len(captured)-1] != "memcached-3.1.5" {
		t.Fatalf("包名未按白名单透传: %v", captured)
	}
	captured = nil
	if _, err := mgr.RemovePackages(prefix, []string{"redis", "memcached-3.1.5"}); err != nil {
		t.Fatalf("remove 失败: %v", err)
	}
	joined = strings.Join(captured, " ")
	if strings.Contains(joined, "apt") || strings.Contains(joined, "-g") || captured[0] != "uninstall" {
		t.Fatalf("remove argv 异常: %v", captured)
	}
	if captured[1] != "redis" || captured[2] != "memcached" {
		t.Fatalf("uninstall 未剥掉版本后缀: %v", captured)
	}
}

// TestExtClassifyTimeout 校验超时映射为 E_UPSTREAM（classify 的 ErrExtTimeout 分支）。
func TestExtClassifyTimeout(t *testing.T) {
	fake := &extFake{installErr: ErrExtTimeout}
	_, perr := Call("runtime-php.ext.install", json.RawMessage(`{"packages":["redis"]}`), t.TempDir(), fake)
	if perr == nil || perr.Code != pluginapi.E_UPSTREAM {
		t.Fatalf("超时期望 E_UPSTREAM，得到 %+v", perr)
	}
}

// ---- production 等级 fault 注入：四类故障必须全部结构化映射，且空态净化不制造假通过 ----

// extFaultFake 让 ext.install/list/remove 三个执行面方法都返回注入的 FaultError。
type extFaultFake struct {
	stubExecutor
	kind FaultKind
}

func (f extFaultFake) InstallPackages(string, []string) (ExtInstallResult, error) {
	return ExtInstallResult{}, NewFaultError(f.kind, "ext.install 执行面故障")
}
func (f extFaultFake) ListPackages(string) (ExtListResult, error) {
	return ExtListResult{}, NewFaultError(f.kind, "ext.list 执行面故障")
}
func (f extFaultFake) RemovePackages(string, []string) (ExtRemoveResult, error) {
	return ExtRemoveResult{}, NewFaultError(f.kind, "ext.remove 执行面故障")
}

// TestExtFaultInjection 四类故障 × 三方法的 12 组合全覆盖：
// 每格断言结果 nil + 错误码 + 类别关键词，杜绝"永远失败"的假通过。
func TestExtFaultInjection(t *testing.T) {
	kinds := []struct {
		kind     FaultKind
		wantCode string
		wantHint string
	}{
		{FaultKill, pluginapi.E_UPSTREAM, "信号 9"},
		{FaultTimeout, pluginapi.E_UPSTREAM, "超时"},
		{FaultDisk, pluginapi.E_INTERNAL, "磁盘满"},
		{FaultCgroup, pluginapi.E_UNSUPPORTED, "cgroup"},
	}
	methods := []struct {
		method string
		params string
	}{
		{"runtime-php.ext.install", `{"packages":["redis"]}`},
		{"runtime-php.ext.list", `{}`},
		{"runtime-php.ext.remove", `{"packages":["redis"]}`},
	}
	for _, k := range kinds {
		for _, m := range methods {
			k, m := k, m
			t.Run(string(k.kind)+"-"+m.method, func(t *testing.T) {
				root := t.TempDir()
				result, perr := Call(m.method, json.RawMessage(m.params), root, extFaultFake{kind: k.kind})
				if perr == nil {
					t.Fatalf("%s 注入 %s 后未返回错误（结果=%s）", m.method, k.kind, result)
				}
				if result != nil {
					t.Fatalf("%s 出错时不应带结果: %s", m.method, result)
				}
				if perr.Code != k.wantCode || !strings.Contains(perr.Message, k.wantHint) {
					t.Fatalf("%s 注入 %s 错误=%+v，期望 code=%s 含 %q", m.method, k.kind, perr, k.wantCode, k.wantHint)
				}
			})
		}
	}
}

// TestExtFaultSanity 无故障时三方法正常返回，证明 fault 断言不是"永远失败"。
func TestExtFaultSanity(t *testing.T) {
	root := t.TempDir()
	for _, c := range []struct{ method, params string }{
		{"runtime-php.ext.install", `{"packages":["redis"]}`},
		{"runtime-php.ext.list", `{}`},
		{"runtime-php.ext.remove", `{"packages":["redis"]}`},
	} {
		raw, perr := Call(c.method, json.RawMessage(c.params), root, &extFake{})
		if perr != nil || len(raw) == 0 {
			t.Fatalf("无故障时 %s 异常: perr=%+v raw=%s", c.method, perr, raw)
		}
	}
}
