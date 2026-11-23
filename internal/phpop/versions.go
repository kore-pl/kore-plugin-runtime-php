package phpop

import (
	"encoding/json"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// callVersions 处理 runtime-php.versions：无参数，返回宿主全部已安装 PHP。
// 未检测到任何 PHP 时不报错，返回空数组 + count=0 + hint。
func callVersions(params json.RawMessage, exec Executor) (json.RawMessage, *pluginapi.Error) {
	var p versionsParams
	if err := strictUnmarshal(params, &p); err != nil {
		return nil, invalid("runtime-php.versions 参数非法: " + err.Error())
	}
	result, err := exec.Versions()
	if err != nil {
		return nil, classify(err)
	}
	if result.Versions == nil {
		result.Versions = []PHPVersion{}
	}
	result.Count = len(result.Versions)
	if result.Count == 0 {
		result.DefaultVersion = ""
		result.Hint = NoPHPHint
	}
	return marshal(result)
}
