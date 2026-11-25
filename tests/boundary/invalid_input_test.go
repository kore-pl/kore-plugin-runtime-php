package boundary

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/pengtang1024/kore-plugin-runtime-php/internal/phpop"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestInvalidInputRejectsMalformedParams 覆盖异常输入：非法 JSON 载荷、未知字段、
// 非法版本格式、越权/穿越 root、未声明方法。用例复用既有 call_test.go 的三组场景
// （TestCallParamValidation 12 例 / TestCallPathsRejectsBadParams 4 例 / TestCallUnknownMethod 4 例），
// 在协议方法边界上再断言一次「错误码明确 + 不返回半成品结果」。
func TestInvalidInputRejectsMalformedParams(t *testing.T) {
	state := t.TempDir()
	traversal := filepath.Join(state, "..", "outside")
	cases := []struct {
		name   string
		method string
		params string
		code   string
	}{
		// 非对象载荷：数组 / null / 空 / 标量都必须是 E_INVALID，而不是被当成零值入参放行。
		{"versions-array", "runtime-php.versions", `[]`, pluginapi.E_INVALID},
		{"versions-null", "runtime-php.versions", `null`, pluginapi.E_INVALID},
		{"versions-empty", "runtime-php.versions", ``, pluginapi.E_INVALID},
		{"versions-scalar", "runtime-php.versions", `"8.5"`, pluginapi.E_INVALID},
		// 未知字段：DisallowUnknownFields 必须拒绝，防参数面静默扩张。
		{"versions-unknown-field", "runtime-php.versions", `{"detail":true}`, pluginapi.E_INVALID},
		{"paths-unknown-field", "runtime-php.paths", `{"verbose":true}`, pluginapi.E_INVALID},
		// 非法版本格式与类型：只接受 X.Y 或 X.Y.Z。
		{"info-version-alpha", "runtime-php.info", `{"version":"abc"}`, pluginapi.E_INVALID},
		{"info-version-v-prefix", "runtime-php.info", `{"version":"v8.5"}`, pluginapi.E_INVALID},
		{"info-version-leading-space", "runtime-php.info", `{"version":" 8.5"}`, pluginapi.E_INVALID},
		{"info-version-four-parts", "runtime-php.info", `{"version":"8.5.4.1"}`, pluginapi.E_INVALID},
		{"info-version-number", "runtime-php.info", `{"version":8.5}`, pluginapi.E_INVALID},
		{"info-unknown-field", "runtime-php.info", `{"extra":1}`, pluginapi.E_INVALID},
		// 越权 root：必须落在白名单内，且拒绝词法穿越、相对路径、反斜杠、不存在目录。
		{"projects-empty-root", "runtime-php.projects", `{}`, pluginapi.E_INVALID},
		{"projects-outside-whitelist", "runtime-php.projects", `{"root":"/etc"}`, pluginapi.E_INVALID},
		{"projects-relative-root", "runtime-php.projects", `{"root":"relative"}`, pluginapi.E_INVALID},
		{"projects-traversal-root", "runtime-php.projects", `{"root":` + jsonString(t, traversal) + `}`, pluginapi.E_INVALID},
		{"projects-backslash-root", "runtime-php.projects", `{"root":` + jsonString(t, state+`\app`) + `}`, pluginapi.E_INVALID},
		{"projects-missing-root", "runtime-php.projects", `{"root":` + jsonString(t, filepath.Join(state, "missing")) + `}`, pluginapi.E_INVALID},
		{"doctor-outside-whitelist", "runtime-php.doctor", `{"root":"/etc"}`, pluginapi.E_INVALID},
		{"doctor-unknown-field", "runtime-php.doctor", `{"unknown":true}`, pluginapi.E_INVALID},
		// 未声明方法 / 跨域方法：必须是 E_NOTFOUND，不能误派发到业务动词。
		{"unknown-method-install", "runtime-php.install", `{}`, pluginapi.E_NOTFOUND},
		{"unknown-method-exec", "runtime-php.exec", `{}`, pluginapi.E_NOTFOUND},
		{"foreign-domain", "php.versions", `{}`, pluginapi.E_NOTFOUND},
		{"bare-prefix", "runtime-php", `{}`, pluginapi.E_NOTFOUND},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, perr := phpop.Call(c.method, json.RawMessage(c.params), state, nil)
			if perr == nil {
				t.Fatalf("%s 接受非法输入 %q，返回结果=%s", c.method, c.params, result)
			}
			if perr.Code != c.code {
				t.Fatalf("%s 错误码=%q，期望 %q（message=%q）", c.method, perr.Code, c.code, perr.Message)
			}
			if result != nil {
				t.Fatalf("%s 出错时必须返回 nil 结果，实际=%s", c.method, result)
			}
			if perr.Message == "" {
				t.Fatalf("%s 错误信息为空（错误码=%q）", c.method, perr.Code)
			}
		})
	}
}
