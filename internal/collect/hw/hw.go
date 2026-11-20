// Package hw 采集深度硬件状态（M4）：EDAC、hwmon/thermal、RAID/DM/块设备、
// PCIe AER、PCI 链路、pstore 与 SMART/NVMe 健康。
//
// 所有数据源都是 best-effort：缺失的硬件不会被当作错误，而是在对应文件里
// 用 present=false 与说明字段表达；只有写包失败才让 Run 返回错误。
// 本包只读 /sys、/proc 与 /dev，不执行任何外部命令。
package hw

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/pack"
)

const (
	// sysPrefix / procPrefix 是只读数据源的根；测试用临时目录替换。
	sysPrefix  = "/sys"
	procPrefix = "/proc"

	// maxEntries 限制 AER/PCI/块设备等列表项数，避免超大机器拖慢采集。
	maxEntries = 512
	// maxSmallFile 是 sysfs 单个标量/文本文件读取上限。
	maxSmallFile = 64 * 1024
	// maxMDStat 限制 /proc/mdstat 的读取量。
	maxMDStat = 64 * 1024
	// maxChannelFiles 限制单个 hwmon 芯片的通道数。
	maxChannelFiles = 256
	// maxPstoreEntries 限制 pstore 索引条目数。
	maxPstoreEntries = 1024
)

// Run 采集全部硬件状态并写入证据包。每个子采集器互相隔离：
// 单个采集器失败只记录 env 错误并继续，写文件失败则终止。
func Run(ctx context.Context, env *collect.Env) error {
	if env == nil {
		return fmt.Errorf("hw: env 为空")
	}

	stages := []struct {
		name string
		rel  string
		fn   func(context.Context) (any, error)
	}{
		{"hardware.edac", "hardware/edac.json", func(context.Context) (any, error) {
			return collectEDAC(sysPrefix), nil
		}},
		{"hardware.hwmon", "hardware/hwmon.json", func(context.Context) (any, error) {
			return collectHwmon(sysPrefix), nil
		}},
		{"hardware.storage", "hardware/storage.json", func(context.Context) (any, error) {
			return collectStorage(sysPrefix, procPrefix), nil
		}},
		{"hardware.aer", "hardware/aer.json", func(context.Context) (any, error) {
			return collectAER(sysPrefix), nil
		}},
		{"hardware.pci", "hardware/pci.json", func(context.Context) (any, error) {
			return collectPCI(sysPrefix), nil
		}},
		{"hardware.pstore", "hardware/pstore.json", func(context.Context) (any, error) {
			return collectPstore(sysPrefix), nil
		}},
		{"hardware.smart", "hardware/smart.json", func(ctx context.Context) (any, error) {
			return collectSMART(ctx, sysPrefix), nil
		}},
	}

	for _, st := range stages {
		if err := ctx.Err(); err != nil {
			return err
		}
		v, err := safeStage(ctx, env, st.name, st.fn)
		if err != nil {
			env.AddError(st.name, "%v", err)
			continue
		}
		if v == nil {
			continue
		}
		if err := pack.WriteJSON(env.Path(st.rel), v); err != nil {
			return fmt.Errorf("%s: %w", st.rel, err)
		}
	}
	return nil
}

// safeStage 隔离单个采集器的 panic，返回 error 由调用方记录。
func safeStage(ctx context.Context, env *collect.Env, name string, fn func(context.Context) (any, error)) (v any, err error) {
	defer func() {
		if r := recover(); r != nil {
			env.AddError(name, "panic: %v", r)
			v, err = nil, nil
		}
	}()
	return fn(ctx)
}

// readFileLimit 读取最多 limit 字节，返回是否被截断。
func readFileLimit(path string, limit int64) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, limit+1))
	truncated := int64(len(buf)) > limit
	if truncated {
		buf = buf[:limit]
	}
	if err != nil {
		return "", truncated, err
	}
	return string(buf), truncated, nil
}

// dirExists 判断路径是否为已存在的目录。
func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// readTrim 读取并去掉首尾空白；失败返回空串。
func readTrim(path string) string {
	s, _, err := readFileLimit(path, maxSmallFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// readInt64 读取一个十进制整数；失败返回 0,false。
func readInt64(path string) (int64, bool) {
	s := readTrim(path)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// readInt64Ptr 读取可选整数，缺失时返回 nil。
func readInt64Ptr(path string) *int64 {
	v, ok := readInt64(path)
	if !ok {
		return nil
	}
	return &v
}

// readUint16File 读取 0..65535 的整数（如 PCI 链路宽度）。
func readUint16File(path string) *int {
	v, ok := readInt64(path)
	if !ok || v < 0 || v > 65535 {
		return nil
	}
	n := int(v)
	return &n
}

// ptrInt/ptrInt64/ptrBool 便于输出 0 值（JSON 里 0 是有意义的事实）。
func ptrInt(v int) *int       { return &v }
func ptrInt64(v int64) *int64 { return &v }
func ptrBool(v bool) *bool    { return &v }

// sortedGlob 返回排序后的 glob 结果，保证输出稳定。
func sortedGlob(pattern string) []string {
	m, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	sort.Strings(m)
	return m
}
