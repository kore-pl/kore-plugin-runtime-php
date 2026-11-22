package fault

import (
	"testing"

	"github.com/pengtang1024/kore-plugin-runtime-php/internal/phpop"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestDiskFullFault 注入磁盘满，期望两方法返回 E_INTERNAL。
func TestDiskFullFault(t *testing.T) {
	assertFault(t, phpop.FaultDisk, pluginapi.E_INTERNAL, "磁盘满")
}
