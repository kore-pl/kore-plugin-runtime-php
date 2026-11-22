package fault

import (
	"testing"

	"github.com/pengtang1024/kore-plugin-runtime-php/internal/phpop"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestCgroupLimitFault 注入 cgroup 超限（memory.max），期望两方法返回 E_UNSUPPORTED。
func TestCgroupLimitFault(t *testing.T) {
	assertFault(t, phpop.FaultCgroup, pluginapi.E_UNSUPPORTED, "cgroup")
}
