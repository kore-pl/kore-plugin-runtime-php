package phpop

import (
	"encoding/json"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// callVersions 处理 runtime-php.versions：校验 params 后返回逻辑版本列表（骨架口径不探测宿主）。
func callVersions(raw json.RawMessage, exec Executor) (json.RawMessage, *pluginapi.Error) {
	var p versionsParams
	if perr := decodeParams(raw, &p); perr != nil {
		return nil, perr
	}
	entries, err := exec.Versions()
	if err != nil {
		return nil, classify(err)
	}
	if entries == nil {
		entries = []VersionEntry{}
	}
	return encode(VersionsResult{Versions: entries, Count: len(entries), Current: defaultVersion})
}

// noopExecutor 是骨架默认执行面：不真起 FPM、不探测宿主，只返回逻辑版本列表。
type noopExecutor struct{}

func (noopExecutor) Versions() ([]VersionEntry, error) {
	out := make([]VersionEntry, 0, len(allowedVersions))
	for _, v := range allowedVersions {
		out = append(out, VersionEntry{Version: v, Active: v == defaultVersion, Installed: true})
	}
	return out, nil
}

func (noopExecutor) Use(version string) (bool, error) { return false, nil }

// decodeParams 校验 params 是 JSON 对象并反序列化到目标形状。
func decodeParams(raw json.RawMessage, target any) *pluginapi.Error {
	if len(raw) == 0 || string(raw) == "null" {
		return invalid("缺少必填参数对象")
	}
	if raw[0] != '{' {
		return invalid("params 必须是 JSON 对象")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return invalid("params 解析失败: " + err.Error())
	}
	return nil
}

// encode 把结果编码为 json.RawMessage；序列化失败归 E_INTERNAL。
func encode(v any) (json.RawMessage, *pluginapi.Error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, internalErr("序列化结果失败: " + err.Error())
	}
	return data, nil
}
