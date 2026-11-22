// Package phpop 实现 runtime-php 插件的方法面：版本号白名单校验、参数校验与结构化结果。
// 本包只做逻辑校验与执行面调用，不真起 FPM 进程、不内置 php/fpm 二进制（骨架口径）。
package phpop

import "fmt"

// FaultKind 标识一次故障注入的类别。
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

// VersionEntry 是可用版本列表中的一项逻辑条目。
type VersionEntry struct {
	Version   string `json:"version"`
	Active    bool   `json:"active"`
	Installed bool   `json:"installed"`
}

// Executor 是 PHP 版本操作的执行面；骨架实现为 noop，测试可注入故障执行器。
// 真实宿主 FPM 调用不在本仓实现，骨架口径下只返回逻辑结果。
type Executor interface {
	Versions() ([]VersionEntry, error)
	Use(version string) (bool, error)
}

// versionsParams / useParams 是两个方法的显式入参形状。
type versionsParams struct {
	Detail bool `json:"detail"`
}

type useParams struct {
	Version string `json:"version"`
}

// VersionsResult / UseResult 是两个方法的结构化结果。
type VersionsResult struct {
	Versions []VersionEntry `json:"versions"`
	Count    int            `json:"count"`
	Current  string         `json:"current"`
}

type UseResult struct {
	Version     string `json:"version"`
	Applied     bool   `json:"applied"`
	KoreManaged bool   `json:"kore_managed"`
}
