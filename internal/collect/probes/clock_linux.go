//go:build linux

package probes

import (
	"syscall"
	"unsafe"
)

const (
	clockRealtime  = 0
	clockMonotonic = 1
)

// readClockPair 通过原始 clock_gettime(2) 同时读取 REALTIME 与 MONOTONIC。
func readClockPair() (clockPair, error) {
	var rt, mono syscall.Timespec
	if _, _, e := syscall.Syscall(sysClockGettime, uintptr(clockRealtime), uintptr(unsafe.Pointer(&rt)), 0); e != 0 {
		return clockPair{}, e
	}
	if _, _, e := syscall.Syscall(sysClockGettime, uintptr(clockMonotonic), uintptr(unsafe.Pointer(&mono)), 0); e != 0 {
		return clockPair{}, e
	}
	return clockPair{RealNS: rt.Nano(), MonoNS: mono.Nano(), Source: "clock_gettime"}, nil
}
