//go:build !linux

package probes

import "time"

// monoEpoch 在进程启动时取一次；time.Time 的单调读数只能通过 Sub 使用。
var monoEpoch = time.Now()

// readClockPair 是非 Linux 构建的回退实现：realtime 用墙钟，
// monotonic 用进程启动以来的单调时长。目标平台为 Linux，此实现只保证可构建。
func readClockPair() (clockPair, error) {
	now := time.Now()
	return clockPair{
		RealNS: now.UnixNano(),
		MonoNS: int64(now.Sub(monoEpoch)),
		Source: "go_time_fallback",
	}, nil
}
