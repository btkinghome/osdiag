package hw

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// aerFile 对应 hardware/aer.json：汇总每个 PCIe 设备的 AER 可纠正/
// fatal/nonfatal 计数器。present=false 表示内核未暴露 AER 计数。
type aerFile struct {
	Present   bool        `json:"present"`
	Devices   []aerDevice `json:"devices"`
	Totals    aerTotals   `json:"totals"`
	Truncated bool        `json:"truncated,omitempty"`
	Notes     []string    `json:"notes,omitempty"`
}

type aerDevice struct {
	PCI         string `json:"pci"`
	Correctable int64  `json:"correctable"`
	Fatal       int64  `json:"fatal"`
	Nonfatal    int64  `json:"nonfatal"`
}

type aerTotals struct {
	Correctable int64 `json:"correctable"`
	Fatal       int64 `json:"fatal"`
	Nonfatal    int64 `json:"nonfatal"`
}

// collectAER 读取 /sys/bus/pci/devices/*/aer_dev_{correctable,fatal,nonfatal}。
// 计数器文件每行形如 "RxErr 3"，其中 TOTAL_ERR_* 行本身是合计，不参与求和。
func collectAER(root string) aerFile {
	out := aerFile{
		Devices: []aerDevice{},
	}
	base := filepath.Join(root, "bus/pci/devices")
	if !dirExists(base) {
		out.Notes = append(out.Notes, "未发现 /sys/bus/pci/devices，无法读取 AER 计数")
		return out
	}
	ents, err := os.ReadDir(base)
	if err != nil {
		out.Notes = append(out.Notes, fmt.Sprintf("读取 PCI 设备目录失败: %v", err))
		return out
	}
	for _, e := range ents {
		if len(out.Devices) >= maxEntries {
			out.Truncated = true
			break
		}
		dir := filepath.Join(base, e.Name())
		cor, okCor := sumCounterFile(filepath.Join(dir, "aer_dev_correctable"))
		fat, okFat := sumCounterFile(filepath.Join(dir, "aer_dev_fatal"))
		non, okNon := sumCounterFile(filepath.Join(dir, "aer_dev_nonfatal"))
		if !okCor && !okFat && !okNon {
			continue
		}
		out.Present = true
		d := aerDevice{PCI: e.Name(), Correctable: cor, Fatal: fat, Nonfatal: non}
		out.Devices = append(out.Devices, d)
		out.Totals.Correctable += cor
		out.Totals.Fatal += fat
		out.Totals.Nonfatal += non
	}
	if !out.Present {
		out.Notes = append(out.Notes, "未发现 aer_dev_* 计数器（可能内核未启用 PCIe AER 或平台无 AER 能力）")
	}
	return out
}

// sumCounterFile 读取并汇总一个 AER 计数器文件。
func sumCounterFile(path string) (int64, bool) {
	s, _, err := readFileLimit(path, 8192)
	if err != nil {
		return 0, false
	}
	return sumCounters(s), true
}

// sumCounters 汇总 "名称 数值" 形式的计数器行。TOTAL_ERR_* 是设备合计，
// 再求和会重复计数，因此跳过；无法解析的行忽略。
func sumCounters(text string) int64 {
	var total int64
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.HasPrefix(fields[0], "TOTAL_ERR") {
			continue
		}
		raw := fields[len(fields)-1]
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			// 只对明确的 0x 前缀做十六进制回退，避免把 "abc" 之类
			// 非法十进制文本误当成十六进制计数。
			hex, ok := strings.CutPrefix(raw, "0x")
			if !ok {
				hex, ok = strings.CutPrefix(raw, "0X")
			}
			if !ok {
				continue
			}
			u, err2 := strconv.ParseUint(hex, 16, 64)
			if err2 != nil {
				continue
			}
			v = int64(u)
		}
		total += v
	}
	return total
}
