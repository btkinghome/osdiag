package hw

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// errSMARTUnsupported 表示设备/平台无法执行 SMART 读取（虚拟盘、USB 桥接、
// 非 Linux 平台等）。这类失败进 unsupported 列表而不是 errors。
var errSMARTUnsupported = errors.New("SMART 不支持")

const (
	// smartMaxDisks 限制每次进程最多读取的物理盘数量。
	smartMaxDisks = 32
	// smartPerDiskTimeout 是单盘 SMART 读取上限。
	smartPerDiskTimeout = 5 * time.Second
)

// smartFile 对应 hardware/smart.json。
type smartFile struct {
	Present     bool               `json:"present"`
	Truncated   bool               `json:"truncated,omitempty"`
	Devices     []smartDevice      `json:"devices"`
	Errors      []smartError       `json:"errors"`
	Unsupported []smartUnsupported `json:"unsupported"`
	Notes       []string           `json:"notes,omitempty"`
}

// smartDevice 是单块盘的健康字段。字段名保持与样例包 smart.json 一致；
// 指针 + omitempty 用来区分“字段不存在”和“值为 0”。
type smartDevice struct {
	Device                string `json:"device"`
	Kind                  string `json:"kind"`
	Model                 string `json:"model,omitempty"`
	Serial                string `json:"serial,omitempty"`
	HealthOK              *bool  `json:"health_ok,omitempty"`
	TemperatureC          *int   `json:"temperature_c,omitempty"`
	PowerOnHours          *int64 `json:"power_on_hours,omitempty"`
	PowerCycles           *int64 `json:"power_cycles,omitempty"`
	UnsafeShutdowns       *int64 `json:"unsafe_shutdowns,omitempty"`
	ReallocatedSectorCt   *int64 `json:"reallocated_sector_ct,omitempty"`
	CurrentPendingSector  *int64 `json:"current_pending_sector,omitempty"`
	OfflineUncorrectable  *int64 `json:"offline_uncorrectable,omitempty"`
	UDMACRCErrorCount     *int64 `json:"udma_crc_error_count,omitempty"`
	ReportedUncorrectable *int64 `json:"reported_uncorrectable,omitempty"`
	MediaErrors           *int64 `json:"media_errors,omitempty"`
	PercentageUsed        *int   `json:"percentage_used,omitempty"`
	AvailableSpare        *int   `json:"available_spare,omitempty"`
	CriticalWarning       *int   `json:"critical_warning,omitempty"`
}

type smartError struct {
	Device string `json:"device"`
	Error  string `json:"error"`
}

type smartUnsupported struct {
	Device string `json:"device"`
	Reason string `json:"reason"`
}

// smartCandidate 是准备探测的一块物理盘。
type smartCandidate struct {
	Name   string
	Path   string
	Kind   string // "ata" | "nvme"
	Model  string
	Serial string
}

// collectSMART 枚举 /sys/block 下的物理盘并逐块读取 SMART/健康日志。
// 单盘失败只记录，不中断；总耗时受 ctx 与单盘 5s 超时约束。
func collectSMART(ctx context.Context, root string) smartFile {
	out := smartFile{
		Devices:     []smartDevice{},
		Errors:      []smartError{},
		Unsupported: []smartUnsupported{},
	}
	if !dirExists(filepath.Join(root, "block")) {
		out.Notes = append(out.Notes, "缺少 /sys/block，无法枚举磁盘")
		return out
	}

	cands, unsupported, notes, truncated := smartCandidates(root)
	out.Unsupported = append(out.Unsupported, unsupported...)
	out.Notes = append(out.Notes, notes...)
	out.Truncated = truncated
	out.Present = len(cands) > 0

	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			out.Notes = append(out.Notes, "采集周期结束，剩余磁盘未读取")
			break
		}
		timeout := smartPerDiskTimeout
		if dl, ok := ctx.Deadline(); ok {
			if rem := time.Until(dl); rem < timeout {
				timeout = rem
			}
		}
		if timeout <= 0 {
			out.Notes = append(out.Notes, "采集周期已到期，剩余磁盘未读取")
			break
		}
		reading, err := safeSMARTProbe(ctx, c, timeout)
		if err != nil {
			if errors.Is(err, errSMARTUnsupported) {
				reason := strings.TrimPrefix(err.Error(), errSMARTUnsupported.Error()+": ")
				out.Unsupported = append(out.Unsupported, smartUnsupported{Device: c.Path, Reason: reason})
			} else {
				out.Errors = append(out.Errors, smartError{Device: c.Path, Error: err.Error()})
			}
			continue
		}
		out.Devices = append(out.Devices, makeSmartDevice(c, reading))
	}
	return out
}

// smartCandidates 从 /sys/block 挑选 ATA/NVMe 物理盘；虚拟盘、USB 桥接、
// MMC 等记入 unsupported。loop/ram/dm/md 等无 SMART 语义的设备静默跳过。
func smartCandidates(root string) (cands []smartCandidate, unsupported []smartUnsupported, notes []string, truncated bool) {
	ents, err := os.ReadDir(filepath.Join(root, "block"))
	if err != nil {
		return nil, nil, []string{fmt.Sprintf("读取 /sys/block 失败: %v", err)}, false
	}
	for _, e := range ents {
		name := e.Name()
		kind := ""
		switch {
		case strings.HasPrefix(name, "nvme"):
			// 只探测命名空间（nvme0n1），跳过纯控制器条目（nvme0）；
			// 命名空间探测会自动改用控制器字符设备读控制器级日志。
			if !strings.Contains(name[len("nvme"):], "n") {
				continue
			}
			kind = "nvme"
		case strings.HasPrefix(name, "sd"), strings.HasPrefix(name, "hd"), strings.HasPrefix(name, "xvd"):
			kind = "ata"
		case strings.HasPrefix(name, "vd"):
			unsupported = append(unsupported, smartUnsupported{Device: "/dev/" + name, Reason: "virtio 虚拟盘，无 SMART 语义"})
			continue
		case strings.HasPrefix(name, "mmcblk"):
			unsupported = append(unsupported, smartUnsupported{Device: "/dev/" + name, Reason: "MMC/SD 设备不支持 ATA/NVMe SMART"})
			continue
		default:
			// loop/ram/zram/dm-/md/sr/fd/nbd/rbd 等：不是物理盘。
			continue
		}

		dir := filepath.Join(root, "block", name)
		if !dirExists(dir) {
			continue
		}
		if kind == "ata" {
			if reason := skipATADevice(dir); reason != "" {
				unsupported = append(unsupported, smartUnsupported{Device: "/dev/" + name, Reason: reason})
				continue
			}
		}
		if len(cands) >= smartMaxDisks {
			truncated = true
			break
		}
		model := readTrim(filepath.Join(dir, "device/model"))
		serial := readTrim(filepath.Join(dir, "device/serial"))
		path := "/dev/" + name
		if kind == "nvme" {
			path = nvmeDevicePath(name)
		}
		cands = append(cands, smartCandidate{
			Name:   name,
			Path:   path,
			Kind:   kind,
			Model:  model,
			Serial: serial,
		})
	}
	if truncated {
		notes = append(notes, fmt.Sprintf("物理盘超过 %d 块，仅读取前 %d 块", smartMaxDisks, smartMaxDisks))
	}
	return cands, unsupported, notes, truncated
}

// skipATADevice 判断 SCSI 磁盘是否应跳过（USB 桥接、明显的虚拟盘）。
func skipATADevice(blockDir string) string {
	devLink := filepath.Join(blockDir, "device")
	if target, err := filepath.EvalSymlinks(devLink); err == nil {
		low := strings.ToLower(target)
		if strings.Contains(low, "/usb") {
			return "USB 桥接设备，跳过以避免挂起"
		}
	}
	model := strings.ToLower(readTrim(filepath.Join(devLink, "model")))
	vendor := strings.ToLower(readTrim(filepath.Join(devLink, "vendor")))
	joined := vendor + " " + model
	for _, marker := range []string{"virtual disk", "virtual hd", "vmware", "qemu", "vbox", " hyper-v", "msft"} {
		if strings.Contains(joined, marker) {
			return "虚拟磁盘（" + strings.TrimSpace(joined) + "），无真实 SMART 数据"
		}
	}
	return ""
}

// nvmeDevicePath 优先使用控制器字符设备 /dev/nvmeX（SMART 是控制器级日志）。
func nvmeDevicePath(blockName string) string {
	idx := strings.LastIndex(blockName, "n")
	if idx > len("nvme") {
		ctrl := "/dev/" + blockName[:idx]
		if st, err := os.Stat(ctrl); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			return ctrl
		}
	}
	return "/dev/" + blockName
}

// safeSMARTProbe 隔离单盘探测的 panic（ioctl 结构体/驱动异常时不影响整体）。
func safeSMARTProbe(ctx context.Context, c smartCandidate, timeout time.Duration) (r smartReading, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("SMART 探测 panic: %v", p)
		}
	}()
	return probeSMARTDevice(ctx, c.Path, c.Kind, timeout)
}

// makeSmartDevice 把平台无关的读数与 sysfs 身份信息合成输出结构。
func makeSmartDevice(c smartCandidate, r smartReading) smartDevice {
	d := smartDevice{
		Device:                c.Path,
		Kind:                  c.Kind,
		Model:                 c.Model,
		Serial:                c.Serial,
		HealthOK:              r.HealthOK,
		TemperatureC:          r.TemperatureC,
		PowerOnHours:          r.PowerOnHours,
		PowerCycles:           r.PowerCycles,
		UnsafeShutdowns:       r.UnsafeShutdowns,
		ReallocatedSectorCt:   r.ReallocatedSectorCt,
		CurrentPendingSector:  r.CurrentPendingSector,
		OfflineUncorrectable:  r.OfflineUncorrectable,
		UDMACRCErrorCount:     r.UDMACRCErrorCount,
		ReportedUncorrectable: r.ReportedUncorrectable,
		MediaErrors:           r.MediaErrors,
		PercentageUsed:        r.PercentageUsed,
		AvailableSpare:        r.AvailableSpare,
		CriticalWarning:       r.CriticalWarning,
	}
	return d
}
