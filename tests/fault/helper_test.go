// Package fault 对 runtime-php 插件做四类故障注入（kill / timeout / disk / cgroup）。
// 每个用例向 phpop.Call 的执行面注入一种故障，断言四个方法返回的结构化错误码；
// 另外配 sanity 用例（无故障正常返回），防止"永远失败"的假通过。
package fault

import (
	"encoding/json"
	"testing"

	"github.com/pengtang1024/kore-plugin-runtime-php/internal/phpop"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// faultExecutor 按注入类别让每个方法都失败，模拟四类运行时故障。
type faultExecutor struct {
	kind phpop.FaultKind
}

func (f faultExecutor) Versions() (phpop.VersionsResult, error) {
	return phpop.VersionsResult{}, phpop.NewFaultError(f.kind, "versions 执行面故障")
}

func (f faultExecutor) Info(string) (phpop.InfoResult, error) {
	return phpop.InfoResult{}, phpop.NewFaultError(f.kind, "info 执行面故障")
}

func (f faultExecutor) Projects(string) (phpop.ProjectsResult, error) {
	return phpop.ProjectsResult{}, phpop.NewFaultError(f.kind, "projects 执行面故障")
}

func (f faultExecutor) Doctor(string) (phpop.DoctorResult, error) {
	return phpop.DoctorResult{}, phpop.NewFaultError(f.kind, "doctor 执行面故障")
}

func (f faultExecutor) Paths() (phpop.PathsResult, error) {
	return phpop.PathsResult{}, phpop.NewFaultError(f.kind, "paths 执行面故障")
}

// methodCase 是一个待注入故障的方法及其合法参数（root 由 testRoot 动态生成）。
type methodCase struct {
	name   string
	method string
	params func(root string) string
}

// methods 覆盖五个方法；root 使用状态目录白名单（t.TempDir 作为 <state>）。
var methods = []methodCase{
	{"versions", "runtime-php.versions", func(string) string { return `{}` }},
	{"info", "runtime-php.info", func(string) string { return `{}` }},
	{"projects", "runtime-php.projects", func(root string) string {
		return `{"root":` + jsonString(root) + `}`
	}},
	{"doctor", "runtime-php.doctor", func(root string) string {
		return `{"root":` + jsonString(root) + `}`
	}},
	{"paths", "runtime-php.paths", func(string) string { return `{}` }},
}

// assertFault 对每个方法注入故障，断言返回 nil 结果 + 预期协议错误码与类别关键词。
func assertFault(t *testing.T, kind phpop.FaultKind, wantCode, wantHint string) {
	t.Helper()
	root := t.TempDir()
	exec := faultExecutor{kind: kind}
	for _, m := range methods {
		t.Run(m.name, func(t *testing.T) {
			result, perr := phpop.Call(m.method, json.RawMessage(m.params(root)), root, exec)
			if perr == nil {
				t.Fatalf("%s 注入 %s 后未返回错误（结果=%s）", m.method, kind, result)
			}
			if result != nil {
				t.Fatalf("%s 出错时不应带结果: %s", m.method, result)
			}
			if perr.Code != wantCode {
				t.Fatalf("%s 错误码=%q，期望 %q（message=%q）", m.method, perr.Code, wantCode, perr.Message)
			}
			if !contains(perr.Message, wantHint) {
				t.Fatalf("%s 错误信息 %q 未含类别关键词 %q", m.method, perr.Message, wantHint)
			}
		})
	}
}

// okExecutor 是不注入任何故障的执行面：用于 sanity 断言。
// 不依赖宿主是否装了 PHP，保证 sanity 在任何机器上都成立。
type okExecutor struct{}

func (okExecutor) Versions() (phpop.VersionsResult, error) {
	return phpop.VersionsResult{Versions: []phpop.PHPVersion{}}, nil
}

func (okExecutor) Info(string) (phpop.InfoResult, error) {
	return phpop.InfoResult{Version: "8.3.6", PHPPath: "/usr/bin/php", Extensions: []string{"Core"}}, nil
}

func (okExecutor) Projects(string) (phpop.ProjectsResult, error) {
	return phpop.ProjectsResult{Projects: []phpop.Project{}, Warnings: []string{}}, nil
}

func (okExecutor) Doctor(string) (phpop.DoctorResult, error) {
	return phpop.DoctorResult{Checks: []phpop.Check{{Name: "php", Status: phpop.StatusPass, Detail: "ok"}}}, nil
}

func (okExecutor) Paths() (phpop.PathsResult, error) {
	return phpop.PathsResult{
		PathEnv:     []string{"/usr/bin"},
		Executables: []phpop.PathExecutable{{Name: "php", Path: "/usr/bin/php", Found: true}},
		Default:     phpop.DefaultRuntime{Command: "php", Found: true},
		SearchOrder: []phpop.SearchSource{{Source: phpop.SourcePath, Paths: []string{"/usr/bin/php"}}},
	}, nil
}

// sanityNoFault 验证无故障时五方法正常返回，证明故障断言不是"永远失败"的假通过。
func sanityNoFault(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	for _, m := range methods {
		result, perr := phpop.Call(m.method, json.RawMessage(m.params(root)), root, okExecutor{})
		if perr != nil {
			t.Fatalf("无故障时 %s 意外返回错误: %v", m.method, perr)
		}
		if len(result) == 0 {
			t.Fatalf("无故障时 %s 返回空结果", m.method)
		}
	}
}

// verifyErrorContract 断言错误体是标准协议结构（非空 code/message）。
func verifyErrorContract(t *testing.T, perr *pluginapi.Error) {
	t.Helper()
	if perr == nil {
		t.Fatal("错误体为 nil")
	}
	if perr.Code == "" || perr.Message == "" {
		t.Fatalf("错误体字段不完整: %+v", perr)
	}
}

// jsonString 把路径编码为 JSON 字符串字面量（路径里的反斜杠/引号会被正确转义）。
func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// contains 是 strings.Contains 的本地封装，避免每个 fault 文件重复 import strings。
func contains(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
