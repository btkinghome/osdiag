package logs

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"luanbird/osdiag/internal/collect"
)

const (
	// SYSLOG_ACTION_READ_ALL：读取整个内核环形缓冲，不清空。
	syslogActionReadAll = 3
	kmsgMaxBytes        = 4 << 20
	kmsgRelPath         = "logs/kmsg.log"
)

// syslogTrap 返回当前平台 syslog(2) 的系统调用号。Go 标准库没有封装
// klogctl，只能直接下发系统调用号。linux/amd64 与 linux/386 是 103；
// linux/arm64 使用通用系统调用表，syslog 为 116。
func syslogTrap() (uintptr, bool) {
	if runtime.GOOS != "linux" {
		return 0, false
	}
	switch runtime.GOARCH {
	case "amd64", "386":
		return 103, true
	case "arm64":
		return 116, true
	default:
		return 0, false
	}
}

// readKmsg 读取内核环形缓冲。返回值是内核原始字节（保留 "<pri>" 前缀），
// 不做任何格式化；truncated 表示缓冲区被 4MiB 上限截满。
func readKmsg() (data []byte, truncated bool, err error) {
	// 系统调用号只在 Linux 上有意义。在其他平台调用 103 可能是完全
	// 不同的系统调用（darwin 上会直接 SIGSYS），必须先用平台判断。
	trap, ok := syslogTrap()
	if !ok {
		return nil, false, fmt.Errorf("kmsg: 平台 %s/%s 不支持 syslog(2)，跳过", runtime.GOOS, runtime.GOARCH)
	}

	buf := make([]byte, kmsgMaxBytes)
	n, _, errno := syscall.Syscall(
		trap,
		syslogActionReadAll,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	// 内核直接写用户缓冲区；调用返回前不能让 buf 被回收/移动。
	runtime.KeepAlive(buf)
	if errno != 0 {
		return nil, false, fmt.Errorf("syslog(%d): %w", syslogActionReadAll, errno)
	}
	if int64(n) < 0 {
		return nil, false, fmt.Errorf("syslog(%d): 返回 %d", syslogActionReadAll, int64(n))
	}
	if n > uintptr(len(buf)) {
		n = uintptr(len(buf))
	}
	return buf[:n], n >= uintptr(len(buf)), nil
}

// collectKmsg 读取并落盘 kmsg；失败只记录错误，不影响其他日志源。
func collectKmsg(env *collect.Env) (*fileRecord, bool) {
	data, truncated, err := readKmsg()
	if err != nil {
		env.AddError("logs", "%v", err)
		return nil, false
	}
	if len(data) == 0 {
		return nil, false
	}

	dst := env.Path(kmsgRelPath)
	if err := os.MkdirAll(env.Path("logs"), 0o755); err != nil {
		env.AddError("logs", "创建 logs 目录失败: %v", err)
		return nil, false
	}
	// 原样落盘：不能用会补换行的 WriteText。
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		env.AddError("logs", "写 %s 失败: %v", kmsgRelPath, err)
		return nil, false
	}
	rec, err := describeFile(env, dst, kmsgRelPath, "kmsg", truncated, false)
	if err != nil {
		env.AddError("logs", "统计 %s 失败: %v", kmsgRelPath, err)
		return nil, false
	}
	return rec, true
}
