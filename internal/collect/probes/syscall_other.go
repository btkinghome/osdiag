//go:build !linux

package probes

import (
	"crypto/rand"
	"syscall"
	"time"
)

// readRawSyscalls 是非 Linux 构建的回退：getpid 可用，uname 原始调用不可用，
// 随机熵改用 Go 的 crypto/rand（其底层仍是系统调用）。
func readRawSyscalls() rawSyscallData {
	var d rawSyscallData

	d.UnameErrno = "ENOSYS"

	t0 := time.Now()
	d.PID = syscall.Getpid()
	d.PIDDur = time.Since(t0)

	buf := make([]byte, 16)
	t0 = time.Now()
	n, err := rand.Read(buf)
	d.RandomDur = time.Since(t0)
	d.RandomSource = "crypto_rand"
	if err != nil {
		d.RandomErrno = errnoOf(err)
	} else {
		d.RandomBytes = n
	}
	return d
}
