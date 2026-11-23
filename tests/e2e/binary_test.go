package e2e

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-runtime-php/internal/phpop"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestBinaryNoPHPDegraded 用真实插件二进制跑"无 PHP"环境：
// 进程只给空的 HOME 与不存在的 PATH，握手后调用 versions，断言不报错并返回 count=0 + 固定 hint。
// 若宿主在 PATH 之外仍有系统级 php（/usr/bin/php* 等），该模拟不成立，跳过并说明原因。
func TestBinaryNoPHPDegraded(t *testing.T) {
	for _, pattern := range []string{"/usr/bin/php*", "/usr/local/bin/php*", "/opt/php*/bin/php"} {
		if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
			t.Skipf("宿主存在系统级 php %v，无法用空 PATH 模拟未安装", matches)
		}
	}
	root := filepath.Clean(filepath.Join("..", ".."))
	binPath := filepath.Join(t.TempDir(), "runtime-php")
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", binPath, ".")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("构建插件二进制失败: %v\n%s", err, out)
	}
	socketPath := filepath.Join(t.TempDir(), "plugin.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("监听 unix socket 失败: %v", err)
	}
	defer func() { _ = listener.Close() }()
	emptyHome := t.TempDir()
	child := exec.Command(binPath)
	// 只给协议必需变量 + 空 HOME + 不存在的 PATH：插件进程内看不到任何 php。
	child.Env = []string{
		"KORE_PLUGIN_SOCK=" + socketPath,
		"KORE_PLUGIN_ID=runtime-php",
		"KORE_PLUGIN_TOKEN=test-token",
		"KORE_LOG_LEVEL=error",
		"KORE_PHP_ROOT=" + emptyHome,
		"HOME=" + emptyHome,
		"PATH=/nonexistent-kore-test-path",
	}
	var childLog strings.Builder
	child.Stderr = &childLog
	if err := child.Start(); err != nil {
		t.Fatalf("启动插件失败: %v", err)
	}
	defer func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	}()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("接受插件连接失败: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	hello := readRequest(t, conn)
	if hello.Method != "kore.hello" {
		t.Fatalf("首帧 method=%q，期望 kore.hello（stderr=%s）", hello.Method, childLog.String())
	}
	writeResponse(t, conn, hello, json.RawMessage(`{"ok":true,"protocol":"kore.plugin.v1"}`))

	versions := callBinary(t, conn, "runtime-php.versions", `{}`)
	var result struct {
		Versions       []phpVersion `json:"versions"`
		DefaultVersion string       `json:"default_version"`
		Count          int          `json:"count"`
		Hint           string       `json:"hint"`
	}
	if err := json.Unmarshal(versions, &result); err != nil {
		t.Fatalf("解析 versions 结果失败: %v（%s）", err, versions)
	}
	if result.Count != 0 || len(result.Versions) != 0 {
		t.Fatalf("空环境应检测到 0 个 PHP: %s", versions)
	}
	if result.DefaultVersion != "" {
		t.Fatalf("未安装时 default_version 应为空: %s", versions)
	}
	if result.Hint != phpop.NoPHPHint {
		t.Fatalf("hint=%q，期望 %q", result.Hint, phpop.NoPHPHint)
	}
	t.Logf("二进制降级 versions => %s", versions)

	// info 在无 PHP 时按 E_NOTFOUND 降级，而不是进程崩溃或空结果。
	infoReq := &pluginapi.Request{
		Type: pluginapi.TypeRequest, ID: randomID(t), ReqID: "r-info",
		Method: "runtime-php.info", Params: json.RawMessage(`{}`), TimeoutMS: 15000,
	}
	if err := pluginapi.WriteJSON(conn, infoReq); err != nil {
		t.Fatalf("发送 info 失败: %v", err)
	}
	infoResp := readResponse(t, conn)
	if infoResp.Error == nil || infoResp.Error.Code != pluginapi.E_NOTFOUND {
		t.Fatalf("info 错误=%+v，期望 E_NOTFOUND", infoResp.Error)
	}
	t.Logf("二进制降级 info => code=%s message=%s", infoResp.Error.Code, infoResp.Error.Message)

	shutdown := &pluginapi.Request{Type: pluginapi.TypeRequest, ID: randomID(t), ReqID: "r-shutdown", Method: "kore.shutdown"}
	if err := pluginapi.WriteJSON(conn, shutdown); err != nil {
		t.Fatalf("发送 shutdown 失败: %v", err)
	}
	if got := readResponse(t, conn); got.Error != nil {
		t.Fatalf("shutdown 返回错误: %+v", got.Error)
	}
}

// phpVersion 是二进制返回结果中的版本条目镜像（避免 e2e 过早耦合内部结构体）。
type phpVersion struct {
	Version         string `json:"version"`
	Path            string `json:"path"`
	Source          string `json:"source"`
	IsDefault       bool   `json:"is_default"`
	SAPI            string `json:"sapi"`
	ExtensionsCount int    `json:"extensions_count"`
}

// callBinary 发一个业务请求并返回 result 载荷。
func callBinary(t *testing.T, conn net.Conn, method, params string) json.RawMessage {
	t.Helper()
	request := &pluginapi.Request{
		Type: pluginapi.TypeRequest, ID: randomID(t), ReqID: "r-" + method,
		Method: method, Params: json.RawMessage(params), TimeoutMS: 15000,
	}
	if err := pluginapi.WriteJSON(conn, request); err != nil {
		t.Fatalf("发送 %s 失败: %v", method, err)
	}
	response := readResponse(t, conn)
	if response.Error != nil {
		t.Fatalf("%s 返回错误: %+v", method, response.Error)
	}
	return response.Result
}

// readRequest 读一帧并断言是 request。
func readRequest(t *testing.T, conn net.Conn) *pluginapi.Request {
	t.Helper()
	env := readEnvelope(t, conn)
	request, ok := env.(*pluginapi.Request)
	if !ok {
		t.Fatalf("帧类型 %T，期望 request", env)
	}
	return request
}

// readResponse 读一帧并断言是 response。
func readResponse(t *testing.T, conn net.Conn) *pluginapi.Response {
	t.Helper()
	env := readEnvelope(t, conn)
	response, ok := env.(*pluginapi.Response)
	if !ok {
		t.Fatalf("帧类型 %T，期望 response", env)
	}
	return response
}

// readEnvelope 读帧并解码为信封。
func readEnvelope(t *testing.T, conn net.Conn) any {
	t.Helper()
	payload, err := pluginapi.ReadFrame(conn, pluginapi.MaxFrameSize)
	if err != nil {
		t.Fatalf("读帧失败: %v", err)
	}
	env, err := pluginapi.DecodeFrame(payload)
	if err != nil {
		t.Fatalf("解码帧失败: %v", err)
	}
	return env
}

// writeResponse 回填 request 的 id/req_id。
func writeResponse(t *testing.T, conn net.Conn, request *pluginapi.Request, result json.RawMessage) {
	t.Helper()
	response := &pluginapi.Response{Type: pluginapi.TypeResponse, ID: request.ID, ReqID: request.ReqID, Result: result}
	if err := pluginapi.WriteJSON(conn, response); err != nil {
		t.Fatalf("写响应失败: %v", err)
	}
}

// randomID 生成 32 位小写 hex 请求 id。
func randomID(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("生成请求 id 失败: %v", err)
	}
	return hex.EncodeToString(buf)
}
