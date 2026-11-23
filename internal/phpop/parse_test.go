package phpop

import "testing"

// TestParsePHPVersion 覆盖 `php -v` 首行的版本解析。
func TestParsePHPVersion(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		want  string
		valid bool
	}{
		{"标准 cli", "PHP 8.3.6 (cli) (built: Jul 11 2024 08:59:00) (NTS)\nCopyright (c) The PHP Group", "8.3.6", true},
		{"新版 8.5", "PHP 8.5.4 (cli) (built: Nov 19 2026 00:22:49) (NTS)", "8.5.4", true},
		{"含调试构建", "PHP 7.4.33 (cli) (built: Nov  2 2022 15:04:30) ( NTS )\nZend Engine v3.4.0", "7.4.33", true},
		{"RC 构建不误判为正式版", "PHP 8.0.0RC1 (cli) (built: Nov 25 2020 00:00:00)", "", false},
		{"只有主次版本", "PHP 8.3 (cli)", "", false},
		{"非 PHP 输出", "hhvm 4.0.0 (cli)", "", false},
		{"空输出", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parsePHPVersion(c.raw)
			if ok != c.valid {
				t.Fatalf("parsePHPVersion(%q) ok=%v，期望 %v", c.raw, ok, c.valid)
			}
			if got != c.want {
				t.Fatalf("parsePHPVersion(%q)=%q，期望 %q", c.raw, got, c.want)
			}
		})
	}
}

// TestParseExtensions 只取 [PHP Modules] 段，忽略 [Zend Modules] 的重复项。
func TestParseExtensions(t *testing.T) {
	raw := `[PHP Modules]
Core
date
json
mbstring
pdo
Zend OPcache

[Zend Modules]
Zend OPcache
`
	got := parseExtensions(raw)
	want := []string{"Core", "date", "json", "mbstring", "pdo", "Zend OPcache"}
	if len(got) != len(want) {
		t.Fatalf("扩展数=%d（%v），期望 %d（%v）", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 项=%q，期望 %q", i, got[i], want[i])
		}
	}
}

// TestParseExtensionsNoHeader 兼容没有段头的精简输出。
func TestParseExtensionsNoHeader(t *testing.T) {
	got := parseExtensions("Core\njson\n\n")
	if len(got) != 2 || got[0] != "Core" || got[1] != "json" {
		t.Fatalf("无段头解析结果=%v", got)
	}
}

// TestParseIniSetting 从 php -i 取有效值（第一段），缺失返回空串。
func TestParseIniSetting(t *testing.T) {
	raw := `phpinfo()
PHP Version => 8.5.4

memory_limit => 256M => 256M
post_max_size => 8M => 8M
max_execution_time => 0 => 30
upload_max_filesize => 2M => 2M
`
	cases := map[string]string{
		"memory_limit":        "256M",
		"post_max_size":       "8M",
		"max_execution_time":  "0",
		"upload_max_filesize": "2M",
		"not_present":         "",
	}
	for key, want := range cases {
		if got := parseIniSetting(raw, key); got != want {
			t.Fatalf("parseIniSetting(%q)=%q，期望 %q", key, got, want)
		}
	}
}

// TestParseIniPath 解析 php --ini 的已加载路径；(none) 返回空串。
func TestParseIniPath(t *testing.T) {
	loaded := `Configuration File (php.ini) Path: "/etc/php/8.5/cli"
Loaded Configuration File:         "/etc/php/8.5/cli/php.ini"
Scan for additional .ini files in: "/etc/php/8.5/cli/conf.d"
Additional .ini files parsed:      (none)`
	if got := parseIniPath(loaded); got != "/etc/php/8.5/cli/php.ini" {
		t.Fatalf("已加载路径=%q", got)
	}
	none := "Configuration File (php.ini) Path: \"/etc/php/8.5/cli\"\nLoaded Configuration File:         (none)"
	if got := parseIniPath(none); got != "" {
		t.Fatalf("未加载时应为空串，实际 %q", got)
	}
	if got := parseIniPath(""); got != "" {
		t.Fatalf("空输出应返回空串，实际 %q", got)
	}
}

// TestParseMemoryLimit 覆盖 M/G/K/B、-1 与非法值。
func TestParseMemoryLimit(t *testing.T) {
	cases := []struct {
		raw       string
		want      int64
		unlimited bool
		ok        bool
	}{
		{"128M", 128 * 1024 * 1024, false, true},
		{"256m", 256 * 1024 * 1024, false, true},
		{"1G", 1024 * 1024 * 1024, false, true},
		{"512K", 512 * 1024, false, true},
		{"1048576", 1048576, false, true},
		{"-1", 0, true, true},
		{"", 0, false, false},
		{"abc", 0, false, false},
	}
	for _, c := range cases {
		got, unlimited, ok := parseMemoryLimit(c.raw)
		if ok != c.ok || unlimited != c.unlimited || got != c.want {
			t.Fatalf("parseMemoryLimit(%q)=(%d,%v,%v)，期望 (%d,%v,%v)",
				c.raw, got, unlimited, ok, c.want, c.unlimited, c.ok)
		}
	}
}

// TestMatchVersionPattern 校验 info 的 version 入参形状。
func TestMatchVersionPattern(t *testing.T) {
	valid := []string{"8.5", "8.5.4", "7.4", "10.0.1"}
	for _, v := range valid {
		if !matchVersionPattern(v) {
			t.Fatalf("matchVersionPattern(%q) 应为 true", v)
		}
	}
	invalid := []string{"", "8", "v8.5", "8.5.4.1", "8.x", " 8.5", "8.5 "}
	for _, v := range invalid {
		if matchVersionPattern(v) {
			t.Fatalf("matchVersionPattern(%q) 应为 false", v)
		}
	}
}
