package phpop

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// stubExecutor 用固定结果替代真实探测，验证协议层结果形状。
type stubExecutor struct {
	versions VersionsResult
	info     InfoResult
	projects ProjectsResult
	doctor   DoctorResult
	paths    PathsResult
	err      error
}

func (s stubExecutor) Versions() (VersionsResult, error) { return s.versions, s.err }
func (s stubExecutor) Info(string) (InfoResult, error)   { return s.info, s.err }
func (s stubExecutor) Projects(string) (ProjectsResult, error) {
	return s.projects, s.err
}
func (s stubExecutor) Doctor(string) (DoctorResult, error) { return s.doctor, s.err }
func (s stubExecutor) Paths() (PathsResult, error)         { return s.paths, s.err }

// TestBusinessVerb 方法名归一化：只接受 runtime-php.<16 个动词>（含 paths / use / guard.* / ext.*）。
func TestBusinessVerb(t *testing.T) {
	cases := map[string]string{
		"runtime-php.versions": "versions",
		"runtime-php.info":     "info",
		"runtime-php.projects": "projects",
		"runtime-php.doctor":   "doctor",
		"runtime-php.paths":    "paths",
		"runtime-php.use":      "use",

		"runtime-php.guard.add":     "guard.add",
		"runtime-php.guard.list":    "guard.list",
		"runtime-php.guard.start":   "guard.start",
		"runtime-php.guard.stop":    "guard.stop",
		"runtime-php.guard.restart": "guard.restart",
		"runtime-php.guard.remove":  "guard.remove",
		"runtime-php.guard.logs":    "guard.logs",

		"runtime-php.ext.install": "ext.install",
		"runtime-php.ext.list":    "ext.list",
		"runtime-php.ext.remove":  "ext.remove",
	}
	for method, want := range cases {
		got, ok := businessVerb(method)
		if !ok || got != want {
			t.Fatalf("businessVerb(%q)=(%q,%v)，期望 (%q,true)", method, got, ok, want)
		}
	}
	for _, method := range []string{"runtime-php.exec", "runtime-php.allocatePort", "php.versions", "versions", "runtime-php.", "runtime-php.guard", "runtime-php.guard.exec"} {
		if _, ok := businessVerb(method); ok {
			t.Fatalf("businessVerb(%q) 不应被接受", method)
		}
	}
}

// TestVersionsResultShape 版本项必须带 sapi=cli 与 extensions_count，空列表归一为 []。
func TestVersionsResultShape(t *testing.T) {
	exec := stubExecutor{versions: VersionsResult{Versions: []PHPVersion{{
		Version: "8.3.6", Path: "/usr/bin/php8.3", Source: SourceKnownPath, IsDefault: true,
		SAPI: SAPI, ExtensionsCount: 12,
	}}, DefaultVersion: "8.3.6"}}
	raw, perr := Call("runtime-php.versions", json.RawMessage(`{}`), "", exec)
	if perr != nil {
		t.Fatalf("versions 失败: %v", perr)
	}
	for _, want := range []string{`"version":"8.3.6"`, `"sapi":"cli"`, `"extensions_count":12`,
		`"source":"known_path"`, `"is_default":true`, `"count":1`, `"default_version":"8.3.6"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("结果缺少 %q: %s", want, raw)
		}
	}

	empty, perr := Call("runtime-php.versions", json.RawMessage(`{}`), "", stubExecutor{})
	if perr != nil {
		t.Fatalf("空 versions 失败: %v", perr)
	}
	if !strings.Contains(string(empty), `"versions":[]`) || !strings.Contains(string(empty), `"hint":`) {
		t.Fatalf("空列表必须序列化为 [] 且带 hint: %s", empty)
	}
	if !strings.Contains(string(empty), `"count":0`) {
		t.Fatalf("count 应为 0: %s", empty)
	}
}

// TestInfoResultShape 详情字段与 ini_settings 四个键齐全。
func TestInfoResultShape(t *testing.T) {
	exec := stubExecutor{info: InfoResult{
		Version:    "8.5.4",
		PHPPath:    "/usr/bin/php8.5",
		IniPath:    "/etc/php/8.5/cli/php.ini",
		Extensions: []string{"Core", "json"},
		IniSettings: IniSettings{
			MemoryLimit: "256M", UploadMaxFilesize: "2M", PostMaxSize: "8M", MaxExecutionTime: "0",
		},
	}}
	raw, perr := Call("runtime-php.info", json.RawMessage(`{}`), "", exec)
	if perr != nil {
		t.Fatalf("info 失败: %v", perr)
	}
	for _, want := range []string{`"version":"8.5.4"`, `"php_path":"/usr/bin/php8.5"`,
		`"ini_path":"/etc/php/8.5/cli/php.ini"`, `"extensions":["Core","json"]`,
		`"memory_limit":"256M"`, `"upload_max_filesize":"2M"`, `"post_max_size":"8M"`, `"max_execution_time":"0"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("结果缺少 %q: %s", want, raw)
		}
	}
}

// TestProjectsResultShape 项目项字段齐全，nil warnings 归一为空数组。
func TestProjectsResultShape(t *testing.T) {
	state := t.TempDir()
	exec := stubExecutor{projects: ProjectsResult{Projects: []Project{{
		Path: state, Name: "acme/app", PHPRequirement: "^8.1",
		DependenciesCount: 3, HasLock: true, Framework: "laravel",
	}}}}
	raw, perr := Call("runtime-php.projects", json.RawMessage(`{"root":`+jsonString(state)+`}`), state, exec)
	if perr != nil {
		t.Fatalf("projects 失败: %v", perr)
	}
	for _, want := range []string{`"name":"acme/app"`, `"php_requirement":"^8.1"`,
		`"dependencies_count":3`, `"has_lock":true`, `"framework":"laravel"`, `"count":1`, `"warnings":[]`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("结果缺少 %q: %s", want, raw)
		}
	}
}

// TestDoctorResultShape ok 由 fail 项推导，空 checks 归一为空数组。
func TestDoctorResultShape(t *testing.T) {
	state := t.TempDir()
	exec := stubExecutor{doctor: DoctorResult{Checks: []Check{
		{Name: "php", Status: StatusPass, Detail: "ok"},
		{Name: "ext_pdo", Status: StatusFail, Detail: "缺 pdo"},
	}}}
	raw, perr := Call("runtime-php.doctor", json.RawMessage(`{"root":`+jsonString(state)+`}`), state, exec)
	if perr != nil {
		t.Fatalf("doctor 失败: %v", perr)
	}
	if !strings.Contains(string(raw), `"ok":false`) {
		t.Fatalf("存在 fail 项时 ok 必须为 false: %s", raw)
	}
	if !strings.Contains(string(raw), `"checks":[`) || !strings.Contains(string(raw), `"status":"fail"`) {
		t.Fatalf("checks 形状异常: %s", raw)
	}
}

// TestCallParamValidation 严格参数校验：非对象、未知字段、非法 version、越权 root 都是 E_INVALID。
func TestCallParamValidation(t *testing.T) {
	state := t.TempDir()
	cases := []struct {
		method string
		params string
	}{
		{"runtime-php.versions", `[]`},
		{"runtime-php.versions", `null`},
		{"runtime-php.versions", `{"detail":true}`},
		{"runtime-php.info", `{"version":"abc"}`},
		{"runtime-php.info", `{"version":"v8.5"}`},
		{"runtime-php.info", `{"version":" 8.5"}`},
		{"runtime-php.info", `{"extra":1}`},
		{"runtime-php.projects", `{}`},
		{"runtime-php.projects", `{"root":"/etc"}`},
		{"runtime-php.projects", `{"root":"relative"}`},
		{"runtime-php.doctor", `{"root":"/etc"}`},
		{"runtime-php.doctor", `{"unknown":true}`},
	}
	for _, c := range cases {
		if _, perr := Call(c.method, json.RawMessage(c.params), state, stubExecutor{}); perr == nil || perr.Code != pluginapi.E_INVALID {
			t.Fatalf("Call(%s,%s) 错误=%v，期望 E_INVALID", c.method, c.params, perr)
		}
	}
}

// TestCallUnknownMethod 未声明的方法与跨域方法一律 E_NOTFOUND。
func TestCallUnknownMethod(t *testing.T) {
	for _, method := range []string{"runtime-php.install", "runtime-php.exec", "php.versions", "runtime-php.guard"} {
		_, perr := Call(method, json.RawMessage(`{}`), "", stubExecutor{})
		if perr == nil || perr.Code != pluginapi.E_NOTFOUND {
			t.Fatalf("Call(%q) 错误=%v，期望 E_NOTFOUND", method, perr)
		}
	}
}

// TestCallNoPHP info 在无 PHP 时按 E_NOTFOUND + 固定 hint 降级。
func TestCallNoPHP(t *testing.T) {
	_, perr := Call("runtime-php.info", json.RawMessage(`{}`), "", stubExecutor{err: ErrPHPNotInstalled})
	if perr == nil || perr.Code != pluginapi.E_NOTFOUND {
		t.Fatalf("错误=%v，期望 E_NOTFOUND", perr)
	}
	if perr.Message != NoPHPHint {
		t.Fatalf("message=%q，期望 %q", perr.Message, NoPHPHint)
	}
}

// TestClassify 四类故障与两类哨兵错误的映射。
func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		code string
		hint string
	}{
		{NewFaultError(FaultKill, "x"), pluginapi.E_UPSTREAM, "信号 9"},
		{NewFaultError(FaultTimeout, "x"), pluginapi.E_UPSTREAM, "超时"},
		{NewFaultError(FaultDisk, "x"), pluginapi.E_INTERNAL, "磁盘满"},
		{NewFaultError(FaultCgroup, "x"), pluginapi.E_UNSUPPORTED, "cgroup"},
		{ErrPHPNotInstalled, pluginapi.E_NOTFOUND, "未检测到 PHP"},
		{ErrVersionNotInstalled, pluginapi.E_INVALID, "指定 PHP 版本未安装"},
		{errors.New("boom"), pluginapi.E_INTERNAL, "执行面失败"},
	}
	for _, c := range cases {
		perr := classify(c.err)
		if perr == nil || perr.Code != c.code || !strings.Contains(perr.Message, c.hint) {
			t.Fatalf("classify(%v)=%+v，期望 code=%q 含 %q", c.err, perr, c.code, c.hint)
		}
	}
}

// jsonString 把路径编码为 JSON 字符串字面量。
func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// TestCallPathsNormalizes 空结果归一：数组字段不为 null，未找到默认 PHP 时补 hint。
func TestCallPathsNormalizes(t *testing.T) {
	raw, perr := Call("runtime-php.paths", json.RawMessage(`{}`), "", stubExecutor{})
	if perr != nil {
		t.Fatalf("paths 意外错误: %v", perr)
	}
	text := string(raw)
	for _, want := range []string{`"path_env":[]`, `"executables":[]`, `"search_order":[]`, `"hint":"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("结果=%s，缺少 %q", text, want)
		}
	}
}

// TestCallPathsRejectsBadParams 参数必须是 JSON 对象、拒绝未知字段。
func TestCallPathsRejectsBadParams(t *testing.T) {
	for _, params := range []string{`[]`, `null`, ``, `{"verbose":true}`} {
		_, perr := Call("runtime-php.paths", json.RawMessage(params), "", stubExecutor{})
		if perr == nil || perr.Code != pluginapi.E_INVALID {
			t.Fatalf("params=%q 错误=%v，期望 E_INVALID", params, perr)
		}
	}
}

// TestCallPathsPropagatesFault 执行面故障映射为协议错误码（paths 与其余方法同一口径）。
func TestCallPathsPropagatesFault(t *testing.T) {
	exec := stubExecutor{err: NewFaultError(FaultTimeout, "paths 超时")}
	_, perr := Call("runtime-php.paths", json.RawMessage(`{}`), "", exec)
	if perr == nil || perr.Code != pluginapi.E_UPSTREAM {
		t.Fatalf("错误=%v，期望 E_UPSTREAM", perr)
	}
}
