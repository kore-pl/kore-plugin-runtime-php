package phpop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// defaultKnownPHPPatterns 是常见安装位置（不依赖 PATH，按 glob 展开）。
var defaultKnownPHPPatterns = []string{
	"/usr/bin/php*",
	"/usr/local/bin/php*",
	"/opt/php*/bin/php",
	"/etc/alternatives/php",
}

// isPHPBaseName 限定 glob 展开结果的 basename：只接受 php / php8 / php8.5 这类名字，
// 排除同一 glob 会命中的 phpize / php-config / phpdbg / phar。
func isPHPBaseName(name string) bool {
	if name == "php" {
		return true
	}
	rest, ok := strings.CutPrefix(name, "php")
	if !ok || rest == "" {
		return false
	}
	for _, part := range strings.Split(rest, ".") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// Runtime 是真实探测执行面：PATH / phpenv / 常见路径 + 固定 argv 命令。
// 字段可注入，便于单测在不碰宿主环境的前提下验证去重、排序与降级逻辑。
type Runtime struct {
	runner     CommandRunner
	lookPath   func(string) (string, error)
	homeDir    func() (string, error)
	glob       func(string) ([]string, error)
	getenv     func(string) string
	knownGlobs []string
}

// NewExecutor 返回生产执行面（真实探测宿主 PHP 安装）。
func NewExecutor() Executor { return newRuntime() }

func newRuntime() *Runtime {
	return &Runtime{
		runner:     osRunner{timeout: CommandTimeout},
		lookPath:   exec.LookPath,
		homeDir:    os.UserHomeDir,
		glob:       filepath.Glob,
		getenv:     os.Getenv,
		knownGlobs: defaultKnownPHPPatterns,
	}
}

// osRunner 是 CommandRunner 的生产实现：无 shell、固定 argv、硬超时。
type osRunner struct {
	timeout time.Duration
}

// Run 执行一条命令并返回去空白的 stdout；超时与退出码非 0 都返回错误。
func (r osRunner) Run(name string, args ...string) (string, error) {
	timeout := r.timeout
	if timeout <= 0 {
		timeout = CommandTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	stdout := newLimitedWriter(MaxCommandOutputBytes)
	stderr := newLimitedWriter(32 * 1024)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("命令 %s 超时（%s）", filepath.Base(name), timeout)
		}
		return strings.TrimSpace(stdout.String()), fmt.Errorf("命令 %s 失败: %w（stderr: %s）",
			filepath.Base(name), err, trimForMessage(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// limitedWriter 是有上限的输出缓冲：超过上限后静默丢弃并置 truncated，
// 避免子进程刷爆插件内存，同时不把正常的大输出（php -i）变成命令失败。
type limitedWriter struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

// newLimitedWriter 构造给定上限的缓冲。
func newLimitedWriter(limit int) *limitedWriter {
	return &limitedWriter{limit: limit}
}

// Write 实现 io.Writer：只在剩余额度内写入，超出部分丢弃并标记截断。
func (w *limitedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buffer.Len()
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = w.buffer.Write(p[:remaining])
		w.truncated = true
		return len(p), nil
	}
	_, _ = w.buffer.Write(p)
	return len(p), nil
}

// String 返回已写入的内容。
func (w *limitedWriter) String() string { return w.buffer.String() }

// trimForMessage 截断过长的 stderr，避免错误信息膨胀。
func trimForMessage(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) > 200 {
		return trimmed[:200] + "…"
	}
	return trimmed
}

// candidate 是一个待验证的 php 可执行文件候选。
type candidate struct {
	path   string
	source Source
}

// Versions 探测宿主全部 PHP 安装并按「默认优先 + 版本降序 + 路径升序」返回。
// 任一候选执行 -v 失败只跳过该候选，不影响其他安装。
func (r *Runtime) Versions() (VersionsResult, error) {
	versions := r.detect()
	defaultVersion := ""
	for _, item := range versions {
		if item.IsDefault {
			defaultVersion = item.Version
			break
		}
	}
	result := VersionsResult{Versions: versions, DefaultVersion: defaultVersion, Count: len(versions)}
	if len(versions) == 0 {
		result.Hint = NoPHPHint
	}
	return result, nil
}

// detect 收集候选、验版本、按真实路径去重后排序。
func (r *Runtime) detect() []PHPVersion {
	candidates := r.candidates()
	seen := map[string]bool{}
	versions := make([]PHPVersion, 0, len(candidates))
	for _, item := range candidates {
		real, ok := resolveCandidate(item.path)
		if !ok || seen[real] {
			continue
		}
		version, err := r.phpVersion(item.path)
		if err != nil {
			continue
		}
		seen[real] = true
		binDir := filepath.Dir(item.path)
		versions = append(versions, PHPVersion{
			Version:            version,
			Path:               item.path,
			Source:             item.source,
			SAPI:               SAPI,
			ExtensionsCount:    len(r.extensions(item.path)),
			BinDir:             binDir,
			PackageManager:     PackageManager,
			PackageManagerPath: r.packageManagerPath(binDir),
		})
	}
	defaultReal := r.defaultRealPath()
	for i := range versions {
		if defaultReal != "" {
			if real, ok := resolveCandidate(versions[i].Path); ok && real == defaultReal {
				versions[i].IsDefault = true
			}
		}
	}
	sortVersions(versions)
	return versions
}

// candidates 按优先级收集候选：PATH → phpenv → 常见路径。
func (r *Runtime) candidates() []candidate {
	out := make([]candidate, 0, 8)
	seen := map[string]bool{}
	add := func(path string, source Source) {
		cleaned := filepath.Clean(strings.TrimSpace(path))
		if cleaned == "" || seen[cleaned] {
			return
		}
		if !isPHPBaseName(filepath.Base(cleaned)) {
			return
		}
		seen[cleaned] = true
		out = append(out, candidate{path: cleaned, source: source})
	}
	for _, name := range runtimeBinaryNames {
		if path, err := r.lookPath(name); err == nil {
			add(path, SourcePath)
		}
	}
	if home, err := r.homeDir(); err == nil && home != "" {
		if matches, globErr := r.glob(filepath.Join(home, ".phpenv", "versions", "*", "bin", "php")); globErr == nil {
			for _, match := range matches {
				add(match, SourcePhpenv)
			}
		}
	}
	for _, pattern := range r.knownGlobs {
		matches, err := r.glob(pattern)
		if err != nil {
			continue
		}
		for _, match := range matches {
			add(match, SourceKnownPath)
		}
	}
	return out
}

// resolveCandidate 跟随符号链接返回真实路径，并确认目标是可执行文件（普通文件）。
func resolveCandidate(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real, true
	}
	return filepath.Clean(path), true
}

// defaultRealPath 按 `which php` 语义（PATH 解析）返回默认 php 的真实路径。
func (r *Runtime) defaultRealPath() string {
	path, err := r.lookPath("php")
	if err != nil || path == "" {
		return ""
	}
	if real, ok := resolveCandidate(path); ok {
		return real
	}
	return ""
}

// packageManagerPath 返回与当前 php 配套的包管理器路径：同目录优先，其次 PATH。
// 只做文件查找（os.Stat / exec.LookPath），不执行任何命令。
func (r *Runtime) packageManagerPath(binDir string) string {
	for _, name := range packageManagerNames {
		sibling := filepath.Join(binDir, name)
		if info, err := os.Stat(sibling); err == nil && !info.IsDir() {
			return sibling
		}
	}
	for _, name := range packageManagerNames {
		if path, err := r.lookPath(name); err == nil && path != "" {
			return path
		}
	}
	return ""
}

// Paths 返回 PATH 解析、默认 PHP 解析链与按优先级排列的发现来源。
// 只读：复用 candidates()/lookPath/glob 与一次 `-v`，不修改宿主状态。
func (r *Runtime) Paths() (PathsResult, error) {
	pathEnv := ""
	if r.getenv != nil {
		pathEnv = r.getenv("PATH")
	}
	result := PathsResult{
		PathEnv:     splitPathEnv(pathEnv),
		Executables: make([]PathExecutable, 0, len(runtimeBinaryNames)),
		SearchOrder: make([]SearchSource, 0, 4),
	}
	for _, name := range runtimeBinaryNames {
		entry := PathExecutable{Name: name}
		if path, err := r.lookPath(name); err == nil && path != "" {
			entry.Path = path
			entry.Found = true
		}
		result.Executables = append(result.Executables, entry)
	}
	result.Default = r.defaultRuntime()
	result.SearchOrder = r.searchOrder()
	if !result.Default.Found {
		result.Hint = NoPHPHint
		if len(result.SearchOrder) > 0 {
			result.Hint = PathDefaultHint
		}
	}
	return result, nil
}

// defaultRuntime 解析当前默认 PHP（which php 语义）并补齐版本与包管理器。
func (r *Runtime) defaultRuntime() DefaultRuntime {
	command := runtimeBinaryNames[0]
	path, err := r.lookPath(command)
	if err != nil || path == "" {
		return DefaultRuntime{Command: command}
	}
	entry := DefaultRuntime{Command: command, Path: path, Found: true}
	if real, ok := resolveCandidate(path); ok {
		entry.RealPath = real
	}
	if version, verr := r.phpVersion(path); verr == nil {
		entry.Version = version
	}
	entry.BinDir = filepath.Dir(path)
	entry.PackageManager = PackageManager
	entry.PackageManagerPath = r.packageManagerPath(entry.BinDir)
	return entry
}

// searchOrder 把 candidates() 按来源分组（保持探测顺序），给出去重后的命中路径。
func (r *Runtime) searchOrder() []SearchSource {
	order := make([]SearchSource, 0, 4)
	index := map[Source]int{}
	for _, item := range r.candidates() {
		if position, ok := index[item.source]; ok {
			order[position].Paths = append(order[position].Paths, item.path)
			continue
		}
		index[item.source] = len(order)
		order = append(order, SearchSource{Source: item.source, Paths: []string{item.path}})
	}
	return order
}

// splitPathEnv 按平台分隔符拆分 PATH，丢弃空项（保留原始顺序）。
func splitPathEnv(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// phpVersion 执行 `<php> -v` 并解析版本号。
func (r *Runtime) phpVersion(path string) (string, error) {
	out, err := r.runner.Run(path, "-v")
	if err != nil {
		return "", err
	}
	version, ok := parsePHPVersion(out)
	if !ok {
		return "", fmt.Errorf("无法解析 %s -v 输出: %q", path, firstLine(out))
	}
	return version, nil
}

// extensions 执行 `<php> -m` 并解析模块清单；失败返回空列表（降级，不放大为整次失败）。
func (r *Runtime) extensions(path string) []string {
	out, err := r.runner.Run(path, "-m")
	if err != nil {
		return []string{}
	}
	return parseExtensions(out)
}

// iniSettings 执行 `<php> -i` 并抽取四条关键配置；失败返回零值设置（降级）。
func (r *Runtime) iniSettings(path string) IniSettings {
	out, err := r.runner.Run(path, "-i")
	if err != nil {
		return IniSettings{}
	}
	return IniSettings{
		MemoryLimit:       parseIniSetting(out, "memory_limit"),
		UploadMaxFilesize: parseIniSetting(out, "upload_max_filesize"),
		PostMaxSize:       parseIniSetting(out, "post_max_size"),
		MaxExecutionTime:  parseIniSetting(out, "max_execution_time"),
	}
}

// iniPath 执行 `<php> --ini` 并取已加载的 ini 路径；未加载返回空串。
func (r *Runtime) iniPath(path string) string {
	out, err := r.runner.Run(path, "--ini")
	if err != nil {
		return ""
	}
	return parseIniPath(out)
}

// Info 汇总单个版本的详情；version 为空时取默认版本。
func (r *Runtime) Info(version string) (InfoResult, error) {
	versions := r.detect()
	if len(versions) == 0 {
		return InfoResult{}, ErrPHPNotInstalled
	}
	entry, ok := selectVersion(versions, version)
	if !ok {
		return InfoResult{}, fmt.Errorf("%w: %s（已检测: %s）", ErrVersionNotInstalled, version, joinVersionList(versions))
	}
	extensions := r.extensions(entry.Path)
	result := InfoResult{
		Version:     entry.Version,
		PHPPath:     entry.Path,
		IniPath:     r.iniPath(entry.Path),
		Extensions:  extensions,
		IniSettings: r.iniSettings(entry.Path),
	}
	return result, nil
}

// selectVersion 选取请求版本：空请求优先默认版本，其次第一个；
// 请求为 `8.5` 这种主次版本时取该分支最高的补丁版本。
func selectVersion(versions []PHPVersion, version string) (PHPVersion, bool) {
	if version == "" {
		for _, item := range versions {
			if item.IsDefault {
				return item, true
			}
		}
		if len(versions) > 0 {
			return versions[0], true
		}
		return PHPVersion{}, false
	}
	for _, item := range versions {
		if item.Version == version {
			return item, true
		}
	}
	best := PHPVersion{}
	found := false
	for _, item := range versions {
		if !strings.HasPrefix(item.Version, version+".") {
			continue
		}
		if !found || compareVersions(item.Version, best.Version) > 0 {
			best = item
			found = true
		}
	}
	return best, found
}

// sortVersions 默认版本排最前，其次版本降序，最后按路径升序（保证结果确定）。
func sortVersions(versions []PHPVersion) {
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].IsDefault != versions[j].IsDefault {
			return versions[i].IsDefault
		}
		if cmp := compareVersions(versions[i].Version, versions[j].Version); cmp != 0 {
			return cmp > 0
		}
		return versions[i].Path < versions[j].Path
	})
}

// joinVersionList 拼接已安装版本号，用于错误信息里的可重试取值。
func joinVersionList(versions []PHPVersion) string {
	out := make([]string, 0, len(versions))
	for _, item := range versions {
		out = append(out, item.Version)
	}
	if len(out) == 0 {
		return "无"
	}
	return strings.Join(out, ",")
}
