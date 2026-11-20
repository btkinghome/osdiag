//go:build linux && amd64

package probes

// syscall 包冻结了旧版 zsysnum 表，amd64 缺少 getrandom；此处补齐。
const (
	sysClockGettime = 228
	sysGetrandom    = 318
)
