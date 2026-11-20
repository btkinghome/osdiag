package hw

import (
	"path/filepath"
)

// edacFile 对应 hardware/edac.json。present=false 表示系统没有 EDAC
// （常见于虚拟机或未加载 edac 模块的机器），不是错误。
type edacFile struct {
	Present     bool             `json:"present"`
	Sources     []string         `json:"sources"`
	Controllers []edacController `json:"controllers"`
	Notes       []string         `json:"notes,omitempty"`
}

type edacController struct {
	Name       string     `json:"name"`
	CECount    int64      `json:"ce_count"`
	UECount    int64      `json:"ue_count"`
	CENoinfo   int64      `json:"ce_noinfo_count"`
	UENoinfo   int64      `json:"ue_noinfo_count"`
	DIMMLabels []string   `json:"dimm_labels"`
	DIMMs      []edacDIMM `json:"dimms"`
}

type edacDIMM struct {
	Name   string `json:"name"`
	Label  string `json:"label,omitempty"`
	SizeMB *int64 `json:"size_mb,omitempty"`
}

// collectEDAC 读取 /sys/devices/system/edac/mc/mc*/ 下的 CE/UE 计数与 DIMM。
func collectEDAC(root string) edacFile {
	out := edacFile{
		Sources:     []string{},
		Controllers: []edacController{},
	}
	mcBase := filepath.Join(root, "devices/system/edac/mc")
	if !dirExists(mcBase) {
		out.Notes = append(out.Notes, "未发现 EDAC mc 目录（可能未加载 edac 模块）")
		return out
	}
	out.Sources = append(out.Sources, mcBase)

	for _, mc := range sortedGlob(filepath.Join(mcBase, "mc*")) {
		if !dirExists(mc) {
			continue
		}
		c := edacController{
			Name:       filepath.Base(mc),
			DIMMLabels: []string{},
			DIMMs:      []edacDIMM{},
		}
		if v, ok := readInt64(filepath.Join(mc, "ce_count")); ok {
			c.CECount = v
		}
		if v, ok := readInt64(filepath.Join(mc, "ue_count")); ok {
			c.UECount = v
		}
		if v, ok := readInt64(filepath.Join(mc, "ce_noinfo_count")); ok {
			c.CENoinfo = v
		}
		if v, ok := readInt64(filepath.Join(mc, "ue_noinfo_count")); ok {
			c.UENoinfo = v
		}
		for _, dm := range sortedGlob(filepath.Join(mc, "dimm*")) {
			d := edacDIMM{Name: filepath.Base(dm)}
			d.Label = readTrim(filepath.Join(dm, "dimm_label"))
			d.SizeMB = readInt64Ptr(filepath.Join(dm, "size"))
			if d.Label != "" {
				c.DIMMLabels = append(c.DIMMLabels, d.Label)
			}
			c.DIMMs = append(c.DIMMs, d)
		}
		out.Controllers = append(out.Controllers, c)
	}
	out.Present = len(out.Controllers) > 0
	if !out.Present {
		out.Notes = append(out.Notes, "EDAC 目录存在但未发现 mc* 控制器（可能未加载 edac 模块）")
	}
	return out
}
