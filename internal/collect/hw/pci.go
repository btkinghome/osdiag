package hw

import (
	"os"
	"path/filepath"
)

// pciFile 对应 hardware/pci.json：PCI 设备标识与当前链路速度/宽度。
// 最多索引 maxEntries 个设备，避免超大拓扑拖慢采集。
type pciFile struct {
	Present   bool        `json:"present"`
	Count     int         `json:"count"`
	Truncated bool        `json:"truncated,omitempty"`
	Devices   []pciDevice `json:"devices"`
}

type pciDevice struct {
	PCI              string `json:"pci"`
	Vendor           string `json:"vendor,omitempty"`
	Device           string `json:"device,omitempty"`
	Class            string `json:"class,omitempty"`
	CurrentLinkSpeed string `json:"current_link_speed,omitempty"`
	CurrentLinkWidth *int   `json:"current_link_width,omitempty"`
}

// collectPCI 读取 /sys/bus/pci/devices/* 的 vendor/device/class 与链路状态。
func collectPCI(root string) pciFile {
	out := pciFile{
		Devices: []pciDevice{},
	}
	base := filepath.Join(root, "bus/pci/devices")
	if !dirExists(base) {
		return out
	}
	out.Present = true
	ents, err := os.ReadDir(base)
	if err != nil {
		return out
	}
	for _, e := range ents {
		if len(out.Devices) >= maxEntries {
			out.Truncated = true
			break
		}
		dir := filepath.Join(base, e.Name())
		d := pciDevice{
			PCI:              e.Name(),
			Vendor:           readTrim(filepath.Join(dir, "vendor")),
			Device:           readTrim(filepath.Join(dir, "device")),
			Class:            readTrim(filepath.Join(dir, "class")),
			CurrentLinkSpeed: readTrim(filepath.Join(dir, "current_link_speed")),
			CurrentLinkWidth: readUint16File(filepath.Join(dir, "current_link_width")),
		}
		out.Devices = append(out.Devices, d)
	}
	out.Count = len(out.Devices)
	return out
}
