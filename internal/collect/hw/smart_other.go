//go:build !linux

package hw

import (
	"context"
	"fmt"
	"time"
)

// probeSMARTDevice 在非 Linux 平台没有可用的 SG_IO/NVMe ioctl，
// 统一按 unsupported 处理（macOS/Windows 目标机不支持本采集路径）。
func probeSMARTDevice(_ context.Context, path, kind string, _ time.Duration) (smartReading, error) {
	return smartReading{}, fmt.Errorf("%w: 非 Linux 平台不提供 SG_IO/NVMe ioctl", errSMARTUnsupported)
}
