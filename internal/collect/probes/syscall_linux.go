//go:build linux

package probes

import (
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// readRawSyscalls 用原始调用执行 uname(2)/getpid(2)/getrandom(2)。
func readRawSyscalls() rawSyscallData {
	var d rawSyscallData

	var uts syscall.Utsname
	t0 := time.Now()
	_, _, e := syscall.Syscall(syscall.SYS_UNAME, uintptr(unsafe.Pointer(&uts)), 0, 0)
	d.UnameDur = time.Since(t0)
	if e != 0 {
		d.UnameErrno = errnoOf(e)
	} else {
		d.UnameSysname = utsField(unsafe.Pointer(&uts.Sysname[0]), len(uts.Sysname))
		d.UnameRelease = utsField(unsafe.Pointer(&uts.Release[0]), len(uts.Release))
		d.UnameMachine = utsField(unsafe.Pointer(&uts.Machine[0]), len(uts.Machine))
	}

	t0 = time.Now()
	pid, _, e := syscall.Syscall(syscall.SYS_GETPID, 0, 0, 0)
	d.PIDDur = time.Since(t0)
	if e != 0 {
		d.PIDErrno = errnoOf(e)
	} else {
		d.PID = int(pid)
	}

	d.RandomSource = "getrandom"
	nr := sysGetrandom
	if nr < 0 {
		d.RandomErrno = "ENOSYS"
		return d
	}
	buf := make([]byte, 16)
	t0 = time.Now()
	n, _, e := syscall.Syscall(uintptr(nr), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
	d.RandomDur = time.Since(t0)
	if e != 0 {
		d.RandomErrno = errnoOf(e)
		return d
	}
	d.RandomBytes = int(n)
	return d
}

// utsField 把 utsname 的 NUL 结尾字符数组转成字符串。
// 通过 unsafe 按字节读取，兼容 int8（amd64/arm64）与 uint8（32 位 ARM）两种字段类型。
func utsField(p unsafe.Pointer, n int) string {
	b := unsafe.Slice((*byte)(p), n)
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}
