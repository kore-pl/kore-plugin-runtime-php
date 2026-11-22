package phpop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestBusinessVerb 方法名归一化：只接受 runtime-php.<verb>，域不符或裸动词一律拒绝。
func TestBusinessVerb(t *testing.T) {
	cases := map[string]string{
		"runtime-php.versions": "versions",
		"runtime-php.use":      "use",
	}
	for method, want := range cases {
		got, ok := businessVerb(method)
		if !ok || got != want {
			t.Fatalf("businessVerb(%q)=(%q,%v)，期望 (%q,true)", method, got, ok, want)
		}
	}
	for _, method := range []string{"runtime-php.exec", "php.versions", "php.use", "versions", "use", "runtime-php."} {
		if _, ok := businessVerb(method); ok {
			t.Fatalf("businessVerb(%q) 不应被接受", method)
		}
	}
}

// TestCallResults 两方法正常路径返回结构化结果，且白名单五个版本齐全。
func TestCallResults(t *testing.T) {
	versions, perr := Call("runtime-php.versions", json.RawMessage(`{}`), nil)
	if perr != nil || !strings.Contains(string(versions), `"count":5`) {
		t.Fatalf("runtime-php.versions 结果=%s err=%v", versions, perr)
	}
	use, perr := Call("runtime-php.use", json.RawMessage(`{"version":"8.1"}`), nil)
	if perr != nil || !strings.Contains(string(use), `"version":"8.1"`) {
		t.Fatalf("runtime-php.use 结果=%s err=%v", use, perr)
	}
	if !strings.Contains(string(use), `"applied":false`) {
		t.Fatalf("骨架口径下 applied 应为 false: %s", use)
	}
}

// TestCallParamValidation 非法参数与超纲版本均返回 E_INVALID。
func TestCallParamValidation(t *testing.T) {
	bad := []struct {
		method string
		params string
	}{
		{"runtime-php.versions", `[]`},
		{"runtime-php.use", `not-json`},
		{"runtime-php.use", `{"version":""}`},
		{"runtime-php.use", `{"version":"5.6"}`},
		{"runtime-php.use", `{"version":"9.0"}`},
		{"runtime-php.use", `{"version":"8.1.2"}`},
	}
	for _, c := range bad {
		_, perr := Call(c.method, json.RawMessage(c.params), nil)
		if perr == nil || perr.Code != pluginapi.E_INVALID {
			t.Fatalf("Call(%s,%s) 错误=%v，期望 E_INVALID", c.method, c.params, perr)
		}
	}
}

// TestCallUnknownMethod 未知方法返回 E_NOTFOUND。
func TestCallUnknownMethod(t *testing.T) {
	_, perr := Call("runtime-php.exec", json.RawMessage(`{}`), nil)
	if perr == nil || perr.Code != pluginapi.E_NOTFOUND {
		t.Fatalf("未知方法错误=%v，期望 E_NOTFOUND", perr)
	}
}

// TestIsAllowed 版本白名单严格匹配五个取值。
func TestIsAllowed(t *testing.T) {
	for _, v := range []string{"7.4", "8.0", "8.1", "8.2", "8.3"} {
		if !IsAllowed(v) {
			t.Fatalf("IsAllowed(%q) 应为 true", v)
		}
	}
	for _, v := range []string{"", "5.6", "7.3", "8.4", "9.0", "8.1.2", "8"} {
		if IsAllowed(v) {
			t.Fatalf("IsAllowed(%q) 应为 false", v)
		}
	}
}
