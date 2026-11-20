//go:build !linux

package probes

import (
	"context"
	"net"
	"time"
)

// icmpPing 在非 Linux 构建上不可用。
func icmpPing(ctx context.Context, dst net.IP, count int, perPacket time.Duration) ([]time.Duration, error) {
	return nil, newErrnoError("ENOSYS", "当前平台未实现原始 ICMP")
}
