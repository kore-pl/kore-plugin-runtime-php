package phpop

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// 必需扩展：缺失即为 fail（主流框架的硬依赖）。
var criticalExtensions = []string{"mbstring", "json", "pdo", "openssl"}

// 推荐扩展：缺失为 warn（常用但可裁剪）。
var recommendedExtensions = []string{"curl", "fileinfo"}

// minMemoryBytes 是 memory_limit 的最低要求（128 MiB）。
const minMemoryBytes = 128 * 1024 * 1024

// callDoctor 处理 runtime-php.doctor：root 可选，给出时同样走白名单。
func callDoctor(params json.RawMessage, exec Executor, stateRoot string) (json.RawMessage, *pluginapi.Error) {
	var p doctorParams
	if err := strictUnmarshal(params, &p); err != nil {
		return nil, invalid("runtime-php.doctor 参数非法: " + err.Error())
	}
	root := ""
	if p.Root != "" {
		validated, perr := ValidateRoot(p.Root, stateRoot)
		if perr != nil {
			return nil, perr
		}
		root = validated
	}
	result, err := exec.Doctor(root)
	if err != nil {
		return nil, classify(err)
	}
	if result.Checks == nil {
		result.Checks = []Check{}
	}
	result.OK = true
	for _, check := range result.Checks {
		if check.Status == StatusFail {
			result.OK = false
			break
		}
	}
	return marshal(result)
}

// Doctor 做真实健康检查：PHP 安装 / 版本基线 / 必需扩展 / memory_limit / php.ini 加载，
// root 非空时再检查项目目录。ok = 没有任何 fail 项。
func (r *Runtime) Doctor(root string) (DoctorResult, error) {
	versions := r.detect()
	checks := make([]Check, 0, 12)
	checks = append(checks, phpCheck(versions))
	entry, hasEntry := selectVersion(versions, "")
	if !hasEntry {
		checks = append(checks,
			Check{Name: "version_supported", Status: StatusFail, Detail: NoPHPHint},
			Check{Name: "extensions", Status: StatusFail, Detail: "PHP 未安装，无法读取扩展列表"},
			Check{Name: "memory_limit", Status: StatusFail, Detail: "PHP 未安装，无法读取 memory_limit"},
			Check{Name: "ini_loaded", Status: StatusWarn, Detail: "PHP 未安装，无 php.ini 可检查"},
		)
		return finishDoctor(checks, root, r, versions), nil
	}
	checks = append(checks, versionSupportedCheck(entry.Version))
	extensions := r.extensions(entry.Path)
	checks = append(checks, extensionChecks(extensions)...)
	settings := r.iniSettings(entry.Path)
	checks = append(checks, memoryLimitCheck(settings.MemoryLimit))
	checks = append(checks, iniLoadedCheck(r.iniPath(entry.Path)))
	if len(versions) > 1 {
		checks = append(checks, Check{
			Name:   "path_conflict",
			Status: StatusWarn,
			Detail: fmt.Sprintf("检测到 %d 个 PHP 安装（%s），PATH 默认 %s；请在站点配置里固定 php 路径",
				len(versions), joinVersionList(versions), defaultPathOf(versions)),
		})
	}
	return finishDoctor(checks, root, r, versions), nil
}

// finishDoctor 追加可选的项目目录检查并计算 ok。
func finishDoctor(checks []Check, root string, r *Runtime, versions []PHPVersion) DoctorResult {
	if root != "" {
		checks = append(checks, r.projectRootCheck(root))
	}
	result := DoctorResult{OK: true, Checks: checks}
	for _, check := range checks {
		if check.Status == StatusFail {
			result.OK = false
			break
		}
	}
	return result
}

// phpCheck 检查是否至少有一个可用 PHP。
func phpCheck(versions []PHPVersion) Check {
	if len(versions) == 0 {
		return Check{Name: "php", Status: StatusFail, Detail: NoPHPHint}
	}
	return Check{
		Name:   "php",
		Status: StatusPass,
		Detail: fmt.Sprintf("检测到 %d 个 PHP，默认 %s（%s）", len(versions), defaultVersionOf(versions), defaultPathOf(versions)),
	}
}

// versionSupportedCheck 按常见框架下限判定版本：<7.4 fail、7.4–7.x warn、≥8.0 pass。
func versionSupportedCheck(version string) Check {
	triple, ok := versionTriple(version)
	if !ok {
		return Check{Name: "version_supported", Status: StatusWarn, Detail: "无法解析版本号: " + version}
	}
	switch {
	case triple[0] >= 8:
		return Check{Name: "version_supported", Status: StatusPass, Detail: fmt.Sprintf("PHP %s 满足常见框架要求（≥8.0）", version)}
	case triple[0] == 7 && triple[1] >= 4:
		return Check{Name: "version_supported", Status: StatusWarn,
			Detail: fmt.Sprintf("PHP %s 满足 7.4 下限（Laravel 8 / Symfony 5），但低于 8.0 基线；新项目建议 8.1+", version)}
	default:
		return Check{Name: "version_supported", Status: StatusFail,
			Detail: fmt.Sprintf("PHP %s 低于 7.4，主流框架已不支持；建议升级到 8.1+", version)}
	}
}

// extensionChecks 为每个必需/推荐扩展生成一条检查项。
func extensionChecks(installed []string) []Check {
	present := make(map[string]bool, len(installed))
	for _, name := range installed {
		present[strings.ToLower(name)] = true
	}
	checks := make([]Check, 0, len(criticalExtensions)+len(recommendedExtensions))
	for _, name := range criticalExtensions {
		checks = append(checks, extensionCheck(name, present[name], StatusFail))
	}
	for _, name := range recommendedExtensions {
		checks = append(checks, extensionCheck(name, present[name], StatusWarn))
	}
	return checks
}

// extensionCheck 生成单条扩展检查：缺失时按 severity 给状态并附安装命令。
func extensionCheck(name string, present bool, severity string) Check {
	checkName := "ext_" + name
	if present {
		return Check{Name: checkName, Status: StatusPass, Detail: "扩展已加载: " + name}
	}
	return Check{
		Name:   checkName,
		Status: severity,
		Detail: fmt.Sprintf("缺少扩展 %s；Debian/Ubuntu 可执行 apt install php-%s", name, name),
	}
}

// memoryLimitCheck 判定 memory_limit 是否 ≥128 MiB（-1 视为不限制，通过）。
func memoryLimitCheck(value string) Check {
	if strings.TrimSpace(value) == "" {
		return Check{Name: "memory_limit", Status: StatusWarn, Detail: "memory_limit 未知（php -i 执行失败）"}
	}
	bytes, unlimited, ok := parseMemoryLimit(value)
	if !ok {
		return Check{Name: "memory_limit", Status: StatusWarn, Detail: "memory_limit 取值无法解析: " + value}
	}
	if unlimited {
		return Check{Name: "memory_limit", Status: StatusPass, Detail: "memory_limit=-1（不限制）"}
	}
	if bytes >= minMemoryBytes {
		return Check{Name: "memory_limit", Status: StatusPass, Detail: fmt.Sprintf("memory_limit=%s（≥128M）", value)}
	}
	return Check{Name: "memory_limit", Status: StatusFail,
		Detail: fmt.Sprintf("memory_limit=%s 低于 128M；建议在 php.ini 设为 256M（Composer 要求 ≥1.5G 场景另配）", value)}
}

// iniLoadedCheck 检查 php.ini 是否真的被加载。
func iniLoadedCheck(iniPath string) Check {
	if iniPath == "" {
		return Check{Name: "ini_loaded", Status: StatusWarn,
			Detail: "未加载 php.ini（使用编译内置默认值）；可用 PHPRC 或 -c 指定配置目录"}
	}
	if info, err := os.Stat(iniPath); err != nil || info.IsDir() {
		return Check{Name: "ini_loaded", Status: StatusWarn, Detail: "php.ini 路径不可读: " + iniPath}
	}
	return Check{Name: "ini_loaded", Status: StatusPass, Detail: "已加载 php.ini: " + iniPath}
}

// projectRootCheck 检查指定 root 下是否能发现 PHP 项目。
func (r *Runtime) projectRootCheck(root string) Check {
	result, err := r.Projects(root)
	if err != nil {
		return Check{Name: "project_root", Status: StatusFail, Detail: "项目目录扫描失败: " + err.Error()}
	}
	if result.Count == 0 {
		return Check{Name: "project_root", Status: StatusWarn, Detail: "目录下未发现 composer.json: " + root}
	}
	return Check{Name: "project_root", Status: StatusPass, Detail: fmt.Sprintf("目录下发现 %d 个 PHP 项目: %s", result.Count, root)}
}

// defaultVersionOf 返回默认版本号；没有默认时退回第一个。
func defaultVersionOf(versions []PHPVersion) string {
	if entry, ok := selectVersion(versions, ""); ok {
		return entry.Version
	}
	return ""
}

// defaultPathOf 返回默认版本路径。
func defaultPathOf(versions []PHPVersion) string {
	if entry, ok := selectVersion(versions, ""); ok {
		return entry.Path
	}
	return ""
}
