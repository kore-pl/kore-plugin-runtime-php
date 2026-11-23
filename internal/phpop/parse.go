package phpop

import (
	"strconv"
	"strings"
)

// parsePHPVersion 从 `php -v` 的整段输出中解析版本号（`PHP 8.3.6 (cli)` → `8.3.6`）。
// 只认首行的 `PHP x.y.z`；解析失败返回 false（该候选被跳过，不影响其他安装）。
func parsePHPVersion(raw string) (string, bool) {
	line := firstLine(raw)
	rest, ok := strings.CutPrefix(line, "PHP ")
	if !ok {
		return "", false
	}
	token, _, _ := strings.Cut(rest, " ")
	return normalizeVersion(token)
}

// normalizeVersion 校验 x.y.z 三段纯数字并原样返回。
func normalizeVersion(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		return "", false
	}
	for _, part := range parts {
		if part == "" {
			return "", false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return "", false
		}
	}
	return strings.Join(parts, "."), true
}

// matchVersionPattern 校验 info 的 version 入参：接受 `8.5.4` 与 `8.5` 两种写法。
// 首尾空白不在这里放行——callInfo 已拒绝带空白的入参。
func matchVersionPattern(raw string) bool {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 && len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

// firstLine 取多行输出的第一行。
func firstLine(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if idx := strings.IndexByte(trimmed, '\n'); idx >= 0 {
		return strings.TrimSpace(trimmed[:idx])
	}
	return trimmed
}

// parseExtensions 解析 `php -m` 的模块清单。
//
// 只取 `[PHP Modules]` 段内的名字（`[Zend Modules]` 段会被忽略，避免 Zend OPcache 重复计数）；
// 没有段头时退化为"所有非空非段头行"，兼容精简构建。
func parseExtensions(raw string) []string {
	lines := strings.Split(raw, "\n")
	inModules := false
	sawHeader := false
	out := make([]string, 0, len(lines))
	seen := map[string]bool{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			sawHeader = true
			inModules = trimmed == "[PHP Modules]"
			continue
		}
		if sawHeader && !inModules {
			continue
		}
		if seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

// parseIniSetting 从 `php -i` 输出里取某个配置项的有效值（形如 `memory_limit => 256M => 256M`）。
// 取第一段值（当前生效值）；缺失或为空返回空串。
func parseIniSetting(raw, key string) string {
	prefix := key + " => "
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		value, ok := strings.CutPrefix(trimmed, prefix)
		if !ok {
			continue
		}
		current, _, _ := strings.Cut(value, " => ")
		return strings.TrimSpace(current)
	}
	return ""
}

// parseIniPath 从 `php --ini` 输出里取已加载的 ini 路径。
// `(none)` 或缺失返回空串（未加载 php.ini 是合法状态，不算错误）。
func parseIniPath(raw string) string {
	const marker = "Loaded Configuration File:"
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		value, ok := strings.CutPrefix(trimmed, marker)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		if value == "" || value == "(none)" {
			return ""
		}
		return value
	}
	return ""
}

// parseMemoryLimit 解析 php.ini 的 memory_limit 取值（`128M` / `1G` / `512K` / `-1`）。
// unlimited 为 true 表示 `-1`（不限制）；无法解析时 ok=false。
func parseMemoryLimit(raw string) (bytes int64, unlimited bool, ok bool) {
	trimmed := strings.ToUpper(strings.TrimSpace(raw))
	if trimmed == "" {
		return 0, false, false
	}
	if trimmed == "-1" {
		return 0, true, true
	}
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(trimmed, "G"):
		multiplier = 1024 * 1024 * 1024
		trimmed = strings.TrimSuffix(trimmed, "G")
	case strings.HasSuffix(trimmed, "M"):
		multiplier = 1024 * 1024
		trimmed = strings.TrimSuffix(trimmed, "M")
	case strings.HasSuffix(trimmed, "K"):
		multiplier = 1024
		trimmed = strings.TrimSuffix(trimmed, "K")
	case strings.HasSuffix(trimmed, "B"):
		trimmed = strings.TrimSuffix(trimmed, "B")
	}
	value, err := strconv.ParseInt(strings.TrimSpace(trimmed), 10, 64)
	if err != nil || value < 0 {
		return 0, false, false
	}
	return value * multiplier, false, true
}

// versionTriple 解析 x.y.z 为三个整数。
func versionTriple(version string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return out, false
		}
		out[i] = value
	}
	return out, true
}

// compareVersions 比较两个 x.y.z 版本号；无法解析时退回字符串比较。
func compareVersions(a, b string) int {
	left, leftOK := versionTriple(a)
	right, rightOK := versionTriple(b)
	if !leftOK || !rightOK {
		return strings.Compare(a, b)
	}
	for i := 0; i < 3; i++ {
		if left[i] != right[i] {
			if left[i] < right[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}
