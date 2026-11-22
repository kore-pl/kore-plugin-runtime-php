package phpop

import (
	"encoding/json"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// callUse 处理 php.use：校验 version 白名单后返回切换结果（骨架口径不真切 FPM）。
func callUse(raw json.RawMessage, exec Executor) (json.RawMessage, *pluginapi.Error) {
	var p useParams
	if perr := decodeParams(raw, &p); perr != nil {
		return nil, perr
	}
	if perr := ValidateVersion(p.Version); perr != nil {
		return nil, perr
	}
	applied, err := exec.Use(p.Version)
	if err != nil {
		return nil, classify(err)
	}
	return encode(UseResult{Version: p.Version, Applied: applied, KoreManaged: true})
}
