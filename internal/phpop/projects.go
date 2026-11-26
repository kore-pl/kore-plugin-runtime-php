package phpop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// lockFile 是判定 has_lock 的锁文件（Composer 只有 composer.lock 一种）。
const lockFile = "composer.lock"

// frameworkRules 是框架识别规则：按声明顺序匹配 require 里的包名。
var frameworkRules = []struct {
	name     string
	packages []string
}{
	{name: "laravel", packages: []string{"laravel/framework"}},
	{name: "symfony", packages: []string{"symfony/framework-bundle", "symfony/symfony"}},
	{name: "thinkphp", packages: []string{"topthink/framework", "topthink/think"}},
}

// 扫描时跳过的目录：依赖目录、VCS 元数据与隐藏目录（vendor 内含海量第三方 composer.json）。
var skipDirs = []string{"vendor", "node_modules", ".git"}

// callProjects 处理 runtime-php.projects：root 必填且必须在白名单内。
func callProjects(params json.RawMessage, exec Executor, stateRoot string) (json.RawMessage, *pluginapi.Error) {
	var p projectsParams
	if err := strictUnmarshal(params, &p); err != nil {
		return nil, invalid("runtime-php.projects 参数有误，请检查后重试: " + err.Error())
	}
	root, perr := ValidateRoot(p.Root, stateRoot)
	if perr != nil {
		return nil, perr
	}
	result, err := exec.Projects(root)
	if err != nil {
		return nil, classify(err)
	}
	if result.Projects == nil {
		result.Projects = []Project{}
	}
	if result.Warnings == nil {
		result.Warnings = []string{}
	}
	result.Count = len(result.Projects)
	return marshal(result)
}

// Projects 扫描 root 下深度 ≤3 的 PHP 项目（含 root 自身的 composer.json）。
// 条目数上限 200；composer.json 只读前 64 KiB；坏 JSON / 超大文件跳过并写 warnings。
// 不跟随目录符号链接，不执行项目脚本，只读元数据。
func (r *Runtime) Projects(root string) (ProjectsResult, error) {
	result := ProjectsResult{Projects: []Project{}, Warnings: []string{}}
	scanned := 0
	truncated := false
	var walk func(dir string, depth int) bool
	walk = func(dir string, depth int) bool {
		result.Projects = appendProject(dir, &result)
		if depth >= MaxScanDepth {
			return true
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			addWarning(&result.Warnings, "目录不可读，已跳过: %s", dir)
			return true
		}
		for _, entry := range entries {
			if scanned >= MaxScanEntries {
				truncated = true
				return false
			}
			scanned++
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if isSkippedDir(name) {
				continue
			}
			if !walk(filepath.Join(dir, name), depth+1) {
				return false
			}
		}
		return true
	}
	walk(root, 0)
	if truncated {
		addWarning(&result.Warnings, "扫描条目达到上限 %d，结果可能不完整", MaxScanEntries)
	}
	sort.Slice(result.Projects, func(i, j int) bool { return result.Projects[i].Path < result.Projects[j].Path })
	result.Count = len(result.Projects)
	return result, nil
}

// isSkippedDir 判定目录是否跳过：依赖目录或隐藏目录。
func isSkippedDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	for _, skipped := range skipDirs {
		if name == skipped {
			return true
		}
	}
	return false
}

// appendProject 读取 dir/composer.json 并追加项目；缺失不是项目，坏文件写 warning 后跳过。
func appendProject(dir string, result *ProjectsResult) []Project {
	composerPath := filepath.Join(dir, "composer.json")
	info, err := os.Lstat(composerPath)
	if err != nil || !info.Mode().IsRegular() {
		return result.Projects
	}
	if info.Size() > MaxComposerJSONBytes {
		addWarning(&result.Warnings, "composer.json 超过 %d 字节，已跳过: %s", MaxComposerJSONBytes, composerPath)
		return result.Projects
	}
	data, err := readLimited(composerPath)
	if err != nil {
		addWarning(&result.Warnings, "读取 composer.json 失败，已跳过: %s", composerPath)
		return result.Projects
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		addWarning(&result.Warnings, "composer.json 不是 JSON 对象，已跳过: %s", composerPath)
		return result.Projects
	}
	var manifest composerManifest
	if err := json.Unmarshal(trimmed, &manifest); err != nil {
		addWarning(&result.Warnings, "composer.json 解析失败，已跳过: %s", composerPath)
		return result.Projects
	}
	name := strings.TrimSpace(manifest.Name)
	if name == "" {
		name = filepath.Base(dir)
	}
	return append(result.Projects, Project{
		Path:              dir,
		Name:              name,
		PHPRequirement:    requireString(manifest.Require, "php"),
		DependenciesCount: countPackages(manifest.Require),
		HasLock:           hasLockFile(dir),
		Framework:         detectFramework(manifest.Require),
	})
}

// requireString 取 require 中某个键的字符串值；非字符串/缺失返回空串。
func requireString(require map[string]json.RawMessage, key string) string {
	raw, ok := require[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// countPackages 统计 require 里的第三方依赖数：只算带 vendor 前缀（含 "/"）的包名，
// 排除 php / ext-* / lib-* / composer-* 这类平台需求，避免把"平台约束"当依赖。
func countPackages(require map[string]json.RawMessage) int {
	count := 0
	for name := range require {
		if strings.Contains(name, "/") {
			count++
		}
	}
	return count
}

// detectFramework 按固定顺序识别框架；识别不出返回空串（JSON 里省略该字段）。
func detectFramework(require map[string]json.RawMessage) string {
	for _, rule := range frameworkRules {
		for _, pkg := range rule.packages {
			if _, ok := require[pkg]; ok {
				return rule.name
			}
		}
	}
	return ""
}

// readLimited 只读取文件前 MaxComposerJSONBytes 字节（防巨大文件耗内存）。
func readLimited(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, MaxComposerJSONBytes))
}

// hasLockFile 判断项目目录下是否存在 composer.lock。
func hasLockFile(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, lockFile))
	return err == nil && !info.IsDir()
}

// addWarning 追加一条告警；相同内容只保留一次。
func addWarning(warnings *[]string, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	for _, existing := range *warnings {
		if existing == message {
			return
		}
	}
	*warnings = append(*warnings, message)
}
