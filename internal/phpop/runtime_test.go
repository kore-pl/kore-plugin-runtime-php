package phpop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRunner 是 CommandRunner 的测试替身：按「命令 参数」精确回放输出，并记录调用序列。
type fakeRunner struct {
	outputs map[string]string
	errs    map[string]error
	calls   []string
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	key := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, key)
	if err, ok := f.errs[key]; ok {
		return "", err
	}
	if out, ok := f.outputs[key]; ok {
		return out, nil
	}
	return "", fmt.Errorf("未预期的命令: %s", key)
}

// phpFixture 是一套临时目录里的假 PHP 安装：PATH 软链 + phpenv + /usr/bin 两份。
type phpFixture struct {
	root     string
	path     string
	phpenv   string
	usrdir   string
	composer string
}

// newPHPFixture 建目录与可执行文件（内容为空，只需通过 resolveCandidate 的存在性检查）。
func newPHPFixture(t *testing.T) phpFixture {
	t.Helper()
	root := t.TempDir()
	makeBinary := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("建目录失败: %v", err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("写文件失败: %v", err)
		}
	}
	fixture := phpFixture{
		root:     root,
		path:     filepath.Join(root, "bin", "php"),
		phpenv:   filepath.Join(root, ".phpenv", "versions", "8.1.0", "bin", "php"),
		usrdir:   filepath.Join(root, "usr", "bin"),
		composer: filepath.Join(root, "bin", "composer"),
	}
	makeBinary(fixture.composer)
	makeBinary(filepath.Join(root, "real-php-8.3.6"))
	makeBinary(fixture.phpenv)
	makeBinary(filepath.Join(fixture.usrdir, "php8.2.1"))
	makeBinary(filepath.Join(fixture.usrdir, "php7.4.33"))
	// PATH 上的 php 与 /usr/bin/php8.3.6 指向同一个真实文件，用于验证去重。
	if err := os.MkdirAll(filepath.Dir(fixture.path), 0o755); err != nil {
		t.Fatalf("建 PATH 目录失败: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "real-php-8.3.6"), fixture.path); err != nil {
		t.Fatalf("建软链失败: %v", err)
	}
	if err := os.Link(filepath.Join(root, "real-php-8.3.6"), filepath.Join(fixture.usrdir, "php8.3.6")); err != nil {
		t.Fatalf("建硬链失败: %v", err)
	}
	return fixture
}

// newFixtureRuntime 用假执行面装配 Runtime：PATH、phpenv、$ROOT/usr/bin/php* 三个渠道。
func newFixtureRuntime(fixture phpFixture, runner *fakeRunner) *Runtime {
	return &Runtime{
		runner: runner,
		lookPath: func(name string) (string, error) {
			switch name {
			case "php":
				return fixture.path, nil
			case "composer":
				return fixture.composer, nil
			}
			return "", errors.New("not found")
		},
		homeDir:    func() (string, error) { return fixture.root, nil },
		glob:       filepath.Glob,
		getenv:     func(string) string { return "/usr/local/bin:/usr/bin" },
		knownGlobs: []string{filepath.Join(fixture.usrdir, "php*")},
	}
}

// fixtureRunner 为三个有效候选准备 -v / -m 输出；php7.4.33 的 -v 返回无法解析的内容。
func fixtureRunner(fixture phpFixture) *fakeRunner {
	outputs := map[string]string{
		fixture.path + " -v":                               "PHP 8.3.6 (cli) (built: Jul 11 2024 08:59:00) (NTS)",
		fixture.path + " -m":                               "[PHP Modules]\nCore\njson\nmbstring\n\n[Zend Modules]\nZend OPcache",
		fixture.path + " -i":                               "memory_limit => 256M => 256M\nupload_max_filesize => 2M => 2M\npost_max_size => 8M => 8M\nmax_execution_time => 0 => 30",
		fixture.path + " --ini":                            "Loaded Configuration File:         \"/etc/php/8.3/cli/php.ini\"",
		fixture.phpenv + " -v":                             "PHP 8.1.0 (cli) (built: Jan 1 2023 00:00:00) (NTS)",
		fixture.phpenv + " -m":                             "[PHP Modules]\nCore\njson",
		filepath.Join(fixture.usrdir, "php8.2.1") + " -v":  "PHP 8.2.1 (cli) (built: Jan 1 2023 00:00:00) (NTS)",
		filepath.Join(fixture.usrdir, "php8.2.1") + " -m":  "[PHP Modules]\nCore\njson",
		filepath.Join(fixture.usrdir, "php7.4.33") + " -v": "这不是 PHP 输出",
	}
	return &fakeRunner{outputs: outputs, errs: map[string]error{}}
}

// TestDetectVersions 去重（软链/硬链同一真实文件只留一项）、默认标记、排序与扩展数。
func TestDetectVersions(t *testing.T) {
	fixture := newPHPFixture(t)
	runner := fixtureRunner(fixture)
	runtime := newFixtureRuntime(fixture, runner)

	result, err := runtime.Versions()
	if err != nil {
		t.Fatalf("Versions 失败: %v", err)
	}
	if result.Count != 3 || len(result.Versions) != 3 {
		t.Fatalf("期望 3 个版本（软/硬链去重 + 无法解析的跳过），实际 %d：%+v", result.Count, result.Versions)
	}
	if result.DefaultVersion != "8.3.6" {
		t.Fatalf("default_version=%q，期望 8.3.6", result.DefaultVersion)
	}
	wantOrder := []string{"8.3.6", "8.2.1", "8.1.0"}
	for i, want := range wantOrder {
		if result.Versions[i].Version != want {
			t.Fatalf("第 %d 项版本=%q，期望 %q（顺序应为默认优先 + 版本降序）", i, result.Versions[i].Version, want)
		}
	}
	if !result.Versions[0].IsDefault {
		t.Fatal("PATH 上的 php 应标记为默认")
	}
	for _, item := range result.Versions[1:] {
		if item.IsDefault {
			t.Fatalf("非 PATH 条目不应标记默认: %+v", item)
		}
	}
	if result.Versions[0].Path != fixture.path {
		t.Fatalf("默认条目路径=%q，期望 PATH 路径 %q", result.Versions[0].Path, fixture.path)
	}
	if result.Versions[0].Source != SourcePath {
		t.Fatalf("默认条目 source=%q，期望 path", result.Versions[0].Source)
	}
	if result.Versions[0].SAPI != SAPI {
		t.Fatalf("sapi=%q，期望 cli", result.Versions[0].SAPI)
	}
	if result.Versions[0].ExtensionsCount != 3 {
		t.Fatalf("扩展数=%d，期望 3（[Zend Modules] 不重复计数）", result.Versions[0].ExtensionsCount)
	}
	for _, item := range result.Versions {
		if item.Version == "7.4.33" {
			t.Fatalf("无法解析 -v 的候选不应进入结果: %+v", item)
		}
	}
	if result.Hint != "" {
		t.Fatalf("检测到 PHP 时不应有 hint: %q", result.Hint)
	}
}

// TestVersionsEmpty 宿主无 PHP 时返回空数组 + count=0 + 固定 hint（不报错）。
func TestVersionsEmpty(t *testing.T) {
	runtime := &Runtime{
		runner:     &fakeRunner{outputs: map[string]string{}, errs: map[string]error{}},
		lookPath:   func(string) (string, error) { return "", errors.New("not found") },
		homeDir:    func() (string, error) { return t.TempDir(), nil },
		glob:       filepath.Glob,
		knownGlobs: []string{"/nonexistent-kore-test/php*"},
	}
	result, err := runtime.Versions()
	if err != nil {
		t.Fatalf("无 PHP 时 Versions 不应报错: %v", err)
	}
	if result.Count != 0 || len(result.Versions) != 0 || result.DefaultVersion != "" {
		t.Fatalf("期望空版本列表: %+v", result)
	}
	if result.Hint != NoPHPHint {
		t.Fatalf("hint=%q，期望 %q", result.Hint, NoPHPHint)
	}
}

// TestInfoSelectsDefault 详情取默认版本，ini 路径与四条配置来自真实命令输出。
func TestInfoSelectsDefault(t *testing.T) {
	fixture := newPHPFixture(t)
	runtime := newFixtureRuntime(fixture, fixtureRunner(fixture))
	info, err := runtime.Info("")
	if err != nil {
		t.Fatalf("Info 失败: %v", err)
	}
	if info.Version != "8.3.6" || info.PHPPath != fixture.path {
		t.Fatalf("版本/路径=%q/%q", info.Version, info.PHPPath)
	}
	if info.IniPath != "/etc/php/8.3/cli/php.ini" {
		t.Fatalf("ini_path=%q", info.IniPath)
	}
	if len(info.Extensions) != 3 || info.Extensions[0] != "Core" {
		t.Fatalf("extensions=%v", info.Extensions)
	}
	if info.IniSettings.MemoryLimit != "256M" || info.IniSettings.UploadMaxFilesize != "2M" ||
		info.IniSettings.PostMaxSize != "8M" || info.IniSettings.MaxExecutionTime != "0" {
		t.Fatalf("ini_settings=%+v", info.IniSettings)
	}
}

// TestInfoMajorMinor 请求 `8.1` 时取该分支最高的补丁版本。
func TestInfoMajorMinor(t *testing.T) {
	fixture := newPHPFixture(t)
	runtime := newFixtureRuntime(fixture, fixtureRunner(fixture))
	info, err := runtime.Info("8.1")
	if err != nil {
		t.Fatalf("Info(8.1) 失败: %v", err)
	}
	if info.Version != "8.1.0" {
		t.Fatalf("版本=%q，期望 8.1.0", info.Version)
	}
	if _, err := runtime.Info("9.9"); !errors.Is(err, ErrVersionNotInstalled) {
		t.Fatalf("未安装版本错误=%v，期望 ErrVersionNotInstalled", err)
	}
}

// TestInfoDegradedIni ini 相关命令失败时降级为空值，不影响版本与扩展。
func TestInfoDegradedIni(t *testing.T) {
	fixture := newPHPFixture(t)
	runner := fixtureRunner(fixture)
	runner.errs[fixture.path+" -i"] = errors.New("boom")
	runner.errs[fixture.path+" --ini"] = errors.New("boom")
	runtime := newFixtureRuntime(fixture, runner)
	info, err := runtime.Info("")
	if err != nil {
		t.Fatalf("Info 失败: %v", err)
	}
	if info.IniPath != "" || info.IniSettings != (IniSettings{}) {
		t.Fatalf("ini 降级不彻底: ini_path=%q settings=%+v", info.IniPath, info.IniSettings)
	}
	if info.Version != "8.3.6" || len(info.Extensions) != 3 {
		t.Fatalf("版本/扩展不应受 ini 失败影响: %+v", info)
	}
}

// TestInfoNoPHP 无 PHP 时 Info 返回 ErrPHPNotInstalled（callInfo 映射为 E_NOTFOUND）。
func TestInfoNoPHP(t *testing.T) {
	runtime := &Runtime{
		runner:     &fakeRunner{outputs: map[string]string{}, errs: map[string]error{}},
		lookPath:   func(string) (string, error) { return "", errors.New("not found") },
		homeDir:    func() (string, error) { return t.TempDir(), nil },
		glob:       filepath.Glob,
		knownGlobs: []string{"/nonexistent-kore-test/php*"},
	}
	if _, err := runtime.Info(""); !errors.Is(err, ErrPHPNotInstalled) {
		t.Fatalf("错误=%v，期望 ErrPHPNotInstalled", err)
	}
}

// TestDoctorReportsMissingExtensions 缺 mbstring/pdo → fail，缺 curl → warn，memory_limit 达标 → pass。
func TestDoctorReportsMissingExtensions(t *testing.T) {
	fixture := newPHPFixture(t)
	runner := fixtureRunner(fixture)
	// 只保留 Core/json/mbstring：pdo/openssl 缺失（fail）；curl/fileinfo 缺失（warn）。
	runner.outputs[fixture.path+" -m"] = "[PHP Modules]\nCore\njson\nmbstring"
	runtime := newFixtureRuntime(fixture, runner)
	result, err := runtime.Doctor("")
	if err != nil {
		t.Fatalf("Doctor 失败: %v", err)
	}
	if result.OK {
		t.Fatal("存在 fail 项时 ok 必须为 false")
	}
	statuses := map[string]Check{}
	for _, check := range result.Checks {
		if check.Name == "" || check.Detail == "" {
			t.Fatalf("检查项字段不完整: %+v", check)
		}
		switch check.Status {
		case StatusPass, StatusWarn, StatusFail:
		default:
			t.Fatalf("status 非法: %+v", check)
		}
		statuses[check.Name] = check
	}
	for _, name := range []string{"php", "version_supported", "memory_limit", "ini_loaded"} {
		if _, ok := statuses[name]; !ok {
			t.Fatalf("缺少检查项 %q: %+v", name, statuses)
		}
	}
	if statuses["ext_pdo"].Status != StatusFail || statuses["ext_openssl"].Status != StatusFail {
		t.Fatalf("必需扩展缺失应为 fail: %+v / %+v", statuses["ext_pdo"], statuses["ext_openssl"])
	}
	if statuses["ext_curl"].Status != StatusWarn || statuses["ext_fileinfo"].Status != StatusWarn {
		t.Fatalf("推荐扩展缺失应为 warn: %+v / %+v", statuses["ext_curl"], statuses["ext_fileinfo"])
	}
	if statuses["ext_mbstring"].Status != StatusPass {
		t.Fatalf("已加载扩展应为 pass: %+v", statuses["ext_mbstring"])
	}
	if statuses["memory_limit"].Status != StatusPass {
		t.Fatalf("256M 应通过 memory_limit 检查: %+v", statuses["memory_limit"])
	}
}

// TestDoctorNoPHP 无 PHP 时 ok=false，且给出安装提示。
func TestDoctorNoPHP(t *testing.T) {
	runtime := &Runtime{
		runner:     &fakeRunner{outputs: map[string]string{}, errs: map[string]error{}},
		lookPath:   func(string) (string, error) { return "", errors.New("not found") },
		homeDir:    func() (string, error) { return t.TempDir(), nil },
		glob:       filepath.Glob,
		knownGlobs: []string{"/nonexistent-kore-test/php*"},
	}
	result, err := runtime.Doctor("")
	if err != nil {
		t.Fatalf("Doctor 失败: %v", err)
	}
	if result.OK {
		t.Fatal("无 PHP 时 ok 必须为 false")
	}
	if len(result.Checks) == 0 {
		t.Fatal("checks 为空")
	}
	if result.Checks[0].Name != "php" || result.Checks[0].Status != StatusFail {
		t.Fatalf("首项应为 php fail: %+v", result.Checks[0])
	}
	if !strings.Contains(result.Checks[0].Detail, "apt install php-cli") {
		t.Fatalf("detail 应给出安装指引: %q", result.Checks[0].Detail)
	}
}

// TestVersionSupported grading：8.x pass、7.4 warn、7.3 fail。
func TestVersionSupported(t *testing.T) {
	if got := versionSupportedCheck("8.3.6"); got.Status != StatusPass {
		t.Fatalf("8.3.6 状态=%q，期望 pass", got.Status)
	}
	if got := versionSupportedCheck("7.4.33"); got.Status != StatusWarn {
		t.Fatalf("7.4.33 状态=%q，期望 warn", got.Status)
	}
	if got := versionSupportedCheck("7.3.0"); got.Status != StatusFail {
		t.Fatalf("7.3.0 状态=%q，期望 fail", got.Status)
	}
}

// TestMemoryLimitCheck 覆盖不限制、达标、不达标与未知。
func TestMemoryLimitCheck(t *testing.T) {
	cases := map[string]string{
		"-1":   StatusPass,
		"256M": StatusPass,
		"128M": StatusPass,
		"64M":  StatusFail,
		"":     StatusWarn,
		"abc":  StatusWarn,
	}
	for value, want := range cases {
		if got := memoryLimitCheck(value); got.Status != want {
			t.Fatalf("memoryLimitCheck(%q)=%q，期望 %q", value, got.Status, want)
		}
	}
}

// TestIsPHPBaseName 只接受 php / php<major>[.<minor>...]，排除 phpize / php-config / phpdbg / phar。
func TestIsPHPBaseName(t *testing.T) {
	valid := []string{"php", "php8", "php8.5", "php8.5.4"}
	for _, name := range valid {
		if !isPHPBaseName(name) {
			t.Fatalf("isPHPBaseName(%q) 应为 true", name)
		}
	}
	invalid := []string{"", "phpize", "php-config", "phpdbg", "phar", "php8.x", "php8.5.beta", "my-php"}
	for _, name := range invalid {
		if isPHPBaseName(name) {
			t.Fatalf("isPHPBaseName(%q) 应为 false", name)
		}
	}
}

// TestLimitedWriter 超过上限后静默截断（Write 仍返回成功），保证交付给 cmd 时不报错。
func TestLimitedWriter(t *testing.T) {
	writer := newLimitedWriter(10)
	if _, err := writer.Write([]byte("0123456789")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if writer.truncated {
		t.Fatal("恰好等于上限时不应标记截断")
	}
	if n, err := writer.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("超限写入应静默丢弃: n=%d err=%v", n, err)
	}
	if !writer.truncated {
		t.Fatal("超限写入应标记截断")
	}
	if got := writer.String(); got != "0123456789" {
		t.Fatalf("内容=%q，期望保留前 10 字节", got)
	}
}

// TestRunnerTruncatesHugeOutput rogue php 刷巨量输出时只捕获上限内容且命令不失败。
func TestRunnerTruncatesHugeOutput(t *testing.T) {
	if _, err := exec.LookPath("head"); err != nil {
		t.Skip("本机没有 head，跳过真实命令用例")
	}
	script := filepath.Join(t.TempDir(), "rogue-php")
	body := "#!/bin/sh\nyes x | head -c 200000\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("写脚本失败: %v", err)
	}
	runner := osRunner{timeout: 5 * time.Second}
	out, err := runner.Run(script, "-v")
	if err != nil {
		t.Fatalf("超量输出不应让命令失败: %v", err)
	}
	if len(out) > MaxCommandOutputBytes {
		t.Fatalf("捕获 %d 字节，超过上限 %d", len(out), MaxCommandOutputBytes)
	}
}

// TestDetectInstallMetadata versions 增强字段：安装目录、包管理器与包管理器路径。
func TestDetectInstallMetadata(t *testing.T) {
	fixture := newPHPFixture(t)
	runtime := newFixtureRuntime(fixture, fixtureRunner(fixture))
	result, err := runtime.Versions()
	if err != nil {
		t.Fatalf("Versions 失败: %v", err)
	}
	if result.Count != 3 {
		t.Fatalf("期望 3 个版本，实际 %+v", result.Versions)
	}
	for _, item := range result.Versions {
		if item.BinDir != filepath.Dir(item.Path) {
			t.Fatalf("%s 的 bin_dir=%q，期望 %q", item.Version, item.BinDir, filepath.Dir(item.Path))
		}
		if item.PackageManager != PackageManager {
			t.Fatalf("%s 的 package_manager=%q，期望 %q", item.Version, item.PackageManager, PackageManager)
		}
		if item.PackageManagerPath != fixture.composer {
			t.Fatalf("%s 的 package_manager_path=%q，期望 %q（同目录或 PATH 回退）",
				item.Version, item.PackageManagerPath, fixture.composer)
		}
	}
}

// TestPathsReportsSearchOrderAndDefault paths：PATH 顺序、可执行名解析、默认解析链与来源顺序。
func TestPathsReportsSearchOrderAndDefault(t *testing.T) {
	fixture := newPHPFixture(t)
	runtime := newFixtureRuntime(fixture, fixtureRunner(fixture))
	result, err := runtime.Paths()
	if err != nil {
		t.Fatalf("Paths 失败: %v", err)
	}
	if len(result.PathEnv) != 2 || result.PathEnv[0] != "/usr/local/bin" || result.PathEnv[1] != "/usr/bin" {
		t.Fatalf("path_env=%v", result.PathEnv)
	}
	if len(result.Executables) != 1 || !result.Executables[0].Found || result.Executables[0].Path != fixture.path {
		t.Fatalf("executables=%+v", result.Executables)
	}
	if !result.Default.Found || result.Default.Command != "php" || result.Default.Path != fixture.path {
		t.Fatalf("default=%+v", result.Default)
	}
	if result.Default.Version != "8.3.6" {
		t.Fatalf("default.version=%q，期望 8.3.6", result.Default.Version)
	}
	if result.Default.PackageManagerPath != fixture.composer {
		t.Fatalf("default.package_manager_path=%q", result.Default.PackageManagerPath)
	}
	if len(result.SearchOrder) != 3 {
		t.Fatalf("search_order=%+v，期望 path/phpenv/known_path 三条", result.SearchOrder)
	}
	if result.SearchOrder[0].Source != SourcePath || result.SearchOrder[0].Paths[0] != fixture.path {
		t.Fatalf("第一条来源异常: %+v", result.SearchOrder[0])
	}
	// 一致性地基：candidates() 的每条路径都必须出现在 search_order 里。
	listed := map[string]bool{}
	for _, source := range result.SearchOrder {
		for _, path := range source.Paths {
			listed[path] = true
		}
	}
	for _, item := range runtime.candidates() {
		if !listed[item.path] {
			t.Fatalf("候选 %s（source=%s）未出现在 search_order", item.path, item.source)
		}
	}
}

// TestPathsWithoutPHP 未安装 PHP 时 paths 不报错：default.found=false + hint。
func TestPathsWithoutPHP(t *testing.T) {
	runtime := &Runtime{
		runner:     &fakeRunner{outputs: map[string]string{}, errs: map[string]error{}},
		lookPath:   func(string) (string, error) { return "", errors.New("not found") },
		homeDir:    func() (string, error) { return "", errors.New("no home") },
		glob:       func(string) ([]string, error) { return nil, nil },
		getenv:     func(string) string { return "" },
		knownGlobs: nil,
	}
	result, err := runtime.Paths()
	if err != nil {
		t.Fatalf("Paths 失败: %v", err)
	}
	if result.Default.Found || result.Hint != NoPHPHint {
		t.Fatalf("未安装时结果异常: %+v", result)
	}
	if len(result.PathEnv) != 0 || len(result.SearchOrder) != 0 {
		t.Fatalf("空环境结果异常: %+v", result)
	}
}

// TestPathsInstalledButNotOnPath 装了 PHP 但 PATH 上没有 php：hint 指向 PATH 问题而非「未检测到」。
func TestPathsInstalledButNotOnPath(t *testing.T) {
	fixture := newPHPFixture(t)
	runtime := newFixtureRuntime(fixture, fixtureRunner(fixture))
	runtime.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	result, err := runtime.Paths()
	if err != nil {
		t.Fatalf("Paths 失败: %v", err)
	}
	if result.Default.Found {
		t.Fatalf("PATH 无 php 时不应解析出默认条目: %+v", result.Default)
	}
	if len(result.SearchOrder) == 0 {
		t.Fatalf("phpenv/常见路径候选存在时 search_order 不应为空")
	}
	if result.Hint != PathDefaultHint {
		t.Fatalf("hint=%q，期望 %q", result.Hint, PathDefaultHint)
	}
}
