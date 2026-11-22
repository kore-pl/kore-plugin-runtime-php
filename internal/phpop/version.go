package phpop

import "github.com/pengtang1024/kore/pkg/pluginapi"

// allowedVersions 是唯一认可的版本号白名单（骨架口径，不探测宿主真实安装）。
var allowedVersions = []string{"7.4", "8.0", "8.1", "8.2", "8.3"}

// defaultVersion 是骨架口径下的当前逻辑版本。
const defaultVersion = "8.2"

// IsAllowed 判定版本号是否在白名单内。
func IsAllowed(version string) bool {
	for _, v := range allowedVersions {
		if version == v {
			return true
		}
	}
	return false
}

// AllowedVersions 返回白名单副本，调用方修改不影响内部状态。
func AllowedVersions() []string {
	out := make([]string, len(allowedVersions))
	copy(out, allowedVersions)
	return out
}

// ValidateVersion 校验版本号非空且在白名单内，否则返回 E_INVALID。
func ValidateVersion(version string) *pluginapi.Error {
	if version == "" {
		return invalid("缺少必填参数 version")
	}
	if !IsAllowed(version) {
		return invalid("不支持的 PHP 版本 " + version + "，允许: " + joinVersions(allowedVersions))
	}
	return nil
}

// joinVersions 用逗号拼接版本白名单，错误信息里给出可重试的合法取值。
func joinVersions(vs []string) string {
	out := ""
	for i, v := range vs {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
