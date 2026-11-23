package phpop

import (
	"encoding/json"
	"strings"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// callInfo 处理 runtime-php.info：version 可省略（取默认版本），给出时必须已安装。
// version 接受 `8.5.4` 与 `8.5` 两种写法（后者取该分支最高的补丁版本）。
func callInfo(params json.RawMessage, exec Executor) (json.RawMessage, *pluginapi.Error) {
	var p infoParams
	if err := strictUnmarshal(params, &p); err != nil {
		return nil, invalid("runtime-php.info 参数非法: " + err.Error())
	}
	if p.Version != "" {
		trimmed := strings.TrimSpace(p.Version)
		if trimmed != p.Version {
			return nil, invalid("runtime-php.info version 不得含首尾空白: " + p.Version)
		}
		if !matchVersionPattern(trimmed) {
			return nil, invalid("runtime-php.info version 格式非法（期望 X.Y 或 X.Y.Z）: " + p.Version)
		}
		p.Version = trimmed
	}
	result, err := exec.Info(p.Version)
	if err != nil {
		return nil, classify(err)
	}
	if result.Extensions == nil {
		result.Extensions = []string{}
	}
	return marshal(result)
}
