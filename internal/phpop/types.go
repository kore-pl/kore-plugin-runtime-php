// Package phpop 实现 runtime-php 插件的方法面：检测宿主已安装的 PHP CLI、
// 汇总单个版本详情、扫描 PHP 项目与健康检查。
//
// 安全口径：只读——不执行项目脚本、不写宿主文件；所有外部命令固定 argv + 5s 超时；
// root 参数必须落在白名单目录内并做符号链接二次校验。
package phpop

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// 采集与安全常量。
const (
	// CommandTimeout 是单条外部命令（php）的硬超时。
	CommandTimeout = 5 * time.Second
	// MaxComposerJSONBytes 是 composer.json 的读取上限（只读前 64 KiB，防巨大文件）。
	MaxComposerJSONBytes = 64 * 1024
	// MaxCommandOutputBytes 是单条命令捕获的 stdout/stderr 上限（1 MiB）：
	// php -i 正常约 100 KiB，设上限防被替换成 rogue php 时刷爆插件内存（plugins ≤24 MiB）。
	MaxCommandOutputBytes = 1 << 20
	// MaxScanDepth 是项目扫描的目录深度上限（root 为 0，最多再下探 3 层）。
	MaxScanDepth = 3
	// MaxScanEntries 是项目扫描的目录条目上限。
	MaxScanEntries = 200
	// NoPHPHint 是未检测到 PHP 时的降级提示（versions 不报错）。
	NoPHPHint = "未检测到 PHP，可执行 apt install php-cli，或用 phpenv / 官方包安装后把 php 放进 PATH"
	// SAPI 是本插件探测的接口类型：只管理 CLI，不管理 FPM 进程。
	SAPI = "cli"
	// PackageManager 是本插件关联的包管理器名（versions[].package_manager 的固定取值）。
	PackageManager = "composer"
	// PathDefaultHint 是「装了 PHP 但 PATH 上没有 php」时的默认版本解析提示。
	PathDefaultHint = "PATH 上未找到 php；已安装的 PHP 仍在 versions 列表中，但无法确定默认版本，请检查 PATH"
)

// runtimeBinaryNames 是 PATH 解析与 paths 方法共用的候选可执行文件名（按优先级）。
var runtimeBinaryNames = []string{"php"}

// packageManagerNames 是包管理器可执行文件名（同目录优先，其次 PATH）。
var packageManagerNames = []string{"composer"}

// Source 标识一个 PHP 可执行文件的发现渠道。
type Source string

const (
	// SourcePath 表示从 PATH 中找到（php）。
	SourcePath Source = "path"
	// SourceKnownPath 表示从常见安装路径找到（/usr/bin、/usr/local/bin、/opt/php*、/etc/alternatives）。
	SourceKnownPath Source = "known_path"
	// SourcePhpenv 表示从 ~/.phpenv/versions/*/bin/php 找到。
	SourcePhpenv Source = "phpenv"
)

// 健康检查状态取值。
const (
	StatusPass = "pass"
	StatusWarn = "warn"
	StatusFail = "fail"
)

// FaultKind 标识一次故障注入的类别（production 级骨架的故障注入契约）。
type FaultKind string

const (
	FaultNone    FaultKind = ""
	FaultKill    FaultKind = "kill"
	FaultTimeout FaultKind = "timeout"
	FaultDisk    FaultKind = "disk"
	FaultCgroup  FaultKind = "cgroup"
)

// FaultError 是执行面注入的故障错误，Call 将其映射为协议错误码。
type FaultError struct {
	Kind    FaultKind
	Message string
}

// Error 实现 error 接口，输出 "<类别>: <说明>"。
func (e *FaultError) Error() string { return fmt.Sprintf("%s: %s", e.Kind, e.Message) }

// NewFaultError 构造给定类别的故障错误。
func NewFaultError(kind FaultKind, message string) *FaultError {
	return &FaultError{Kind: kind, Message: message}
}

// 执行面哨兵错误：用于把"没装 PHP"和"指定版本没装"映射为不同协议错误码。
var (
	// ErrPHPNotInstalled 表示宿主一个 PHP 都没有。
	ErrPHPNotInstalled = errors.New("未检测到 PHP")
	// ErrVersionNotInstalled 表示请求的版本不在已安装列表内。
	ErrVersionNotInstalled = errors.New("指定 PHP 版本未安装")
)

// CommandRunner 是外部命令执行面的最小契约：固定 argv、无 shell、带超时。
type CommandRunner interface {
	Run(name string, args ...string) (string, error)
}

// Executor 是方法执行面：生产实现真实探测宿主；测试可注入故障执行器。
type Executor interface {
	Versions() (VersionsResult, error)
	Info(version string) (InfoResult, error)
	Projects(root string) (ProjectsResult, error)
	Doctor(root string) (DoctorResult, error)
	Paths() (PathsResult, error)
}

// PHPVersion 是一个已安装的 PHP CLI 可执行文件。
// bin_dir 是安装目录（path 的父目录），package_manager 固定为 composer，
// package_manager_path 优先取同目录 composer、其次 PATH 上的 composer（只查找，不执行命令）。
type PHPVersion struct {
	Version            string `json:"version"`
	Path               string `json:"path"`
	Source             Source `json:"source"`
	IsDefault          bool   `json:"is_default"`
	SAPI               string `json:"sapi"`
	ExtensionsCount    int    `json:"extensions_count"`
	BinDir             string `json:"bin_dir"`
	PackageManager     string `json:"package_manager"`
	PackageManagerPath string `json:"package_manager_path"`
}

// VersionsResult 是 runtime-php.versions 的结构化结果。
// 未检测到任何 PHP 时 versions 为空数组、count=0、hint 给出安装指引（不报错）。
type VersionsResult struct {
	Versions       []PHPVersion `json:"versions"`
	DefaultVersion string       `json:"default_version"`
	PinnedVersion  string       `json:"pinned_version,omitempty"`
	PinnedPath     string       `json:"pinned_path,omitempty"`
	Count          int          `json:"count"`
	Hint           string       `json:"hint,omitempty"`
}

// IniSettings 是本插件关心的四条 php.ini 关键配置（取自 `php -i` 的有效值）。
type IniSettings struct {
	MemoryLimit       string `json:"memory_limit"`
	UploadMaxFilesize string `json:"upload_max_filesize"`
	PostMaxSize       string `json:"post_max_size"`
	MaxExecutionTime  string `json:"max_execution_time"`
}

// InfoResult 是 runtime-php.info 的结构化结果。
// ini 未加载时 ini_path 为空串、ini_settings 取 php -i 的有效值（不放大为整次失败）。
type InfoResult struct {
	Version     string      `json:"version"`
	PHPPath     string      `json:"php_path"`
	IniPath     string      `json:"ini_path"`
	Extensions  []string    `json:"extensions"`
	IniSettings IniSettings `json:"ini_settings"`
}

// Project 是一个含 composer.json 的 PHP 项目条目。
type Project struct {
	Path              string `json:"path"`
	Name              string `json:"name"`
	PHPRequirement    string `json:"php_requirement"`
	DependenciesCount int    `json:"dependencies_count"`
	HasLock           bool   `json:"has_lock"`
	Framework         string `json:"framework,omitempty"`
}

// ProjectsResult 是 runtime-php.projects 的结构化结果。
// 单个 composer.json 超过上限/解析失败时跳过并写入 warnings，不中断整次扫描。
type ProjectsResult struct {
	Projects []Project `json:"projects"`
	Count    int       `json:"count"`
	Warnings []string  `json:"warnings"`
}

// Check 是一条健康检查。
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// DoctorResult 是 runtime-php.doctor 的结构化结果：ok = 没有任何 fail 项。
type DoctorResult struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

// PathExecutable 是 PATH 上某个候选可执行名的解析结果。
type PathExecutable struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Found bool   `json:"found"`
}

// DefaultRuntime 是「当前默认 PHP 是哪个、怎么解析出来的」。
type DefaultRuntime struct {
	Command            string `json:"command"`
	Path               string `json:"path"`
	RealPath           string `json:"real_path"`
	Version            string `json:"version"`
	BinDir             string `json:"bin_dir"`
	PackageManager     string `json:"package_manager"`
	PackageManagerPath string `json:"package_manager_path"`
	Found              bool   `json:"found"`
}

// SearchSource 是一条发现来源及其命中路径（顺序与探测优先级一致）。
type SearchSource struct {
	Source Source   `json:"source"`
	Paths  []string `json:"paths"`
}

// PathsResult 是 runtime-php.paths 的结构化结果：解释「为什么用到了这个版本」。
type PathsResult struct {
	PathEnv     []string         `json:"path_env"`
	Executables []PathExecutable `json:"executables"`
	Default     DefaultRuntime   `json:"default"`
	SearchOrder []SearchSource   `json:"search_order"`
	Hint        string           `json:"hint,omitempty"`
}

// versionsParams / infoParams / projectsParams / doctorParams / pathsParams 是显式入参形状。
type versionsParams struct{}

type pathsParams struct{}

type infoParams struct {
	Version string `json:"version"`
}

type projectsParams struct {
	Root string `json:"root"`
}

type doctorParams struct {
	Root string `json:"root"`
}

// composerManifest 是 composer.json 中本插件关心的字段。
// require 的键是动态包名，这里用 map[string]json.RawMessage 只做解析与取值（不引入 any）。
type composerManifest struct {
	Name    string                     `json:"name"`
	Require map[string]json.RawMessage `json:"require"`
}
