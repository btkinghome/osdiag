//go:build linux && !amd64 && !arm64

package probes

import "syscall"

const (
	sysClockGettime = syscall.SYS_CLOCK_GETTIME
	// 其他架构未声明 getrandom 的调用号；-1 表示走 ENOSYS 分支。
	sysGetrandom = -1
)
