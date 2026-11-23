package phpop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestAllowedRoots 白名单固定含三个默认目录，状态目录追加在后面且去重。
func TestAllowedRoots(t *testing.T) {
	roots := allowedRoots("/var/lib/kore/php")
	if len(roots) != 4 || roots[0] != "/var/www" || roots[1] != "/opt" || roots[2] != "/home" || roots[3] != "/var/lib/kore/php" {
		t.Fatalf("白名单=%v", roots)
	}
	if got := allowedRoots("/home"); len(got) != 3 {
		t.Fatalf("状态目录与默认目录重复时应去重，实际=%v", got)
	}
	if got := allowedRoots("relative/path"); len(got) != 3 {
		t.Fatalf("非绝对状态目录不应进入白名单，实际=%v", got)
	}
}

// TestValidateRootRejects 词法层拒绝：空、空白、NUL、反斜杠、相对路径、..、白名单外。
func TestValidateRootRejects(t *testing.T) {
	state := t.TempDir()
	cases := []string{
		"",
		" /var/www",
		"/var/www ",
		"/var/www\x00x",
		"/var/www\\x",
		"var/www",
		"/etc",
		"/tmp",
		"/var/www/../etc",
	}
	for _, raw := range cases {
		if _, perr := ValidateRoot(raw, state); perr == nil {
			t.Fatalf("ValidateRoot(%q) 应被拒绝", raw)
		} else if perr.Code != pluginapi.E_INVALID {
			t.Fatalf("ValidateRoot(%q) 错误码=%q，期望 E_INVALID", raw, perr.Code)
		}
	}
}

// TestValidateRootAccepts 状态目录与其子目录可通过，且返回真实路径。
func TestValidateRootAccepts(t *testing.T) {
	state := t.TempDir()
	child := filepath.Join(state, "site")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	real, perr := ValidateRoot(child, state)
	if perr != nil {
		t.Fatalf("合法 root 被拒绝: %v", perr)
	}
	wantReal, err := filepath.EvalSymlinks(child)
	if err != nil {
		t.Fatalf("解析期望真实路径失败: %v", err)
	}
	if real != wantReal {
		t.Fatalf("返回路径=%q，期望 %q", real, wantReal)
	}
}

// TestValidateRootMissing 不存在的目录按 E_INVALID 返回，不 panic。
func TestValidateRootMissing(t *testing.T) {
	state := t.TempDir()
	_, perr := ValidateRoot(filepath.Join(state, "does-not-exist"), state)
	if perr == nil || perr.Code != pluginapi.E_INVALID {
		t.Fatalf("不存在目录错误=%v，期望 E_INVALID", perr)
	}
}

// TestValidateRootNotDir root 指向普通文件时按 E_INVALID 返回。
func TestValidateRootNotDir(t *testing.T) {
	state := t.TempDir()
	file := filepath.Join(state, "composer.json")
	if err := os.WriteFile(file, []byte("{}"), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	_, perr := ValidateRoot(file, state)
	if perr == nil || perr.Code != pluginapi.E_INVALID {
		t.Fatalf("普通文件错误=%v，期望 E_INVALID", perr)
	}
}

// TestValidateRootSymlinkEscape 白名单内的软链指向白名单外时必须拒绝（二次归属校验）。
func TestValidateRootSymlinkEscape(t *testing.T) {
	state := t.TempDir()
	escape := filepath.Join(state, "escape")
	if err := os.Symlink("/etc", escape); err != nil {
		t.Skipf("无法创建软链（可能是受限环境）: %v", err)
	}
	_, perr := ValidateRoot(escape, state)
	if perr == nil {
		t.Fatal("软链逃逸应被拒绝")
	}
	if perr.Code != pluginapi.E_INVALID {
		t.Fatalf("错误码=%q，期望 E_INVALID", perr.Code)
	}
}

// TestWithin 基于 Rel 的归属判定不允许前缀误判。
func TestWithin(t *testing.T) {
	if !within("/var/www", "/var/www") || !within("/var/www", "/var/www/site") {
		t.Fatal("自身与子目录应判定为 within")
	}
	if within("/var/www", "/var/www-evil") || within("/var/www", "/var") {
		t.Fatal("前缀相同但不是子目录不应判定为 within")
	}
}
