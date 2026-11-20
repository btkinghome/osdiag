package hw

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// storageFile 对应 hardware/storage.json：mdstat 原文摘要、md 阵列状态、
// device-mapper 设备与 slaves、块设备清单。
type storageFile struct {
	Present      bool          `json:"present"`
	MDStat       mdstatSummary `json:"mdstat"`
	MDArrays     []mdArray     `json:"md_arrays"`
	DMDevices    []dmDevice    `json:"dm_devices"`
	BlockDevices []blockDevice `json:"block_devices"`
}

type mdstatSummary struct {
	Present   bool   `json:"present"`
	Raw       string `json:"raw,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type mdArray struct {
	Name          string `json:"name"`
	Level         string `json:"level,omitempty"`
	State         string `json:"state,omitempty"`
	RaidDisks     *int64 `json:"raid_disks,omitempty"`
	Degraded      *int64 `json:"degraded,omitempty"`
	SyncAction    string `json:"sync_action,omitempty"`
	DisksExpected *int   `json:"disks_expected,omitempty"`
	DisksActive   *int   `json:"disks_active,omitempty"`
	SyncProgress  string `json:"sync_progress,omitempty"`
}

type dmDevice struct {
	Name   string   `json:"name"`
	DMName string   `json:"dm_name,omitempty"`
	UUID   string   `json:"uuid,omitempty"`
	Slaves []string `json:"slaves"`
}

type blockDevice struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Model      string `json:"model,omitempty"`
	Vendor     string `json:"vendor,omitempty"`
	Serial     string `json:"serial,omitempty"`
	Firmware   string `json:"firmware,omitempty"`
	Rotational *bool  `json:"rotational,omitempty"`
	SizeBytes  *int64 `json:"size_bytes,omitempty"`
}

var mdDisksRe = regexp.MustCompile(`\[(\d+)/(\d+)\]`)

// collectStorage 汇总 /proc/mdstat、/sys/block/md*、/sys/block/dm-* 与块设备。
func collectStorage(sysRoot, procRoot string) storageFile {
	out := storageFile{
		MDArrays:     []mdArray{},
		DMDevices:    []dmDevice{},
		BlockDevices: []blockDevice{},
	}

	if raw, truncated, err := readFileLimit(filepath.Join(procRoot, "mdstat"), maxMDStat); err == nil {
		out.MDStat = mdstatSummary{
			Present:   true,
			Raw:       strings.TrimSpace(raw),
			Truncated: truncated,
		}
	}

	byName := map[string]int{}
	for _, dir := range sortedGlob(filepath.Join(sysRoot, "block/md*")) {
		if !dirExists(dir) {
			continue
		}
		arr := mdArray{Name: filepath.Base(dir)}
		if v, ok := readInt64(filepath.Join(dir, "md/raid_disks")); ok {
			arr.RaidDisks = ptrInt64(v)
		}
		if v, ok := readInt64(filepath.Join(dir, "md/degraded")); ok {
			arr.Degraded = ptrInt64(v)
		}
		arr.Level = readTrim(filepath.Join(dir, "md/level"))
		arr.SyncAction = readTrim(filepath.Join(dir, "md/sync_action"))
		byName[arr.Name] = len(out.MDArrays)
		out.MDArrays = append(out.MDArrays, arr)
	}

	for _, parsed := range parseMDStat(out.MDStat.Raw) {
		if i, ok := byName[parsed.Name]; ok {
			cur := &out.MDArrays[i]
			cur.State = parsed.State
			cur.DisksExpected = parsed.DisksExpected
			cur.DisksActive = parsed.DisksActive
			cur.SyncProgress = parsed.SyncProgress
			if cur.Level == "" {
				cur.Level = parsed.Level
			}
			continue
		}
		byName[parsed.Name] = len(out.MDArrays)
		out.MDArrays = append(out.MDArrays, parsed)
	}

	for _, dir := range sortedGlob(filepath.Join(sysRoot, "block/dm-*")) {
		if !dirExists(dir) {
			continue
		}
		dm := dmDevice{Name: filepath.Base(dir), Slaves: []string{}}
		dm.DMName = readTrim(filepath.Join(dir, "dm/name"))
		dm.UUID = readTrim(filepath.Join(dir, "dm/uuid"))
		dm.Slaves = readDirNames(filepath.Join(dir, "slaves"), maxEntries)
		out.DMDevices = append(out.DMDevices, dm)
	}

	ents, err := os.ReadDir(filepath.Join(sysRoot, "block"))
	if err == nil {
		for _, e := range ents {
			if len(out.BlockDevices) >= maxEntries {
				break
			}
			name := e.Name()
			dir := filepath.Join(sysRoot, "block", name)
			if !dirExists(dir) {
				continue
			}
			bd := blockDevice{Name: name, Path: "/dev/" + name}
			devDir := filepath.Join(dir, "device")
			bd.Model = readTrim(filepath.Join(devDir, "model"))
			bd.Vendor = readTrim(filepath.Join(devDir, "vendor"))
			bd.Serial = readTrim(filepath.Join(devDir, "serial"))
			bd.Firmware = readTrim(filepath.Join(devDir, "firmware_rev"))
			if v, ok := readInt64(filepath.Join(dir, "queue/rotational")); ok {
				bd.Rotational = ptrBool(v != 0)
			}
			if v, ok := readInt64(filepath.Join(dir, "size")); ok {
				bd.SizeBytes = ptrInt64(v * 512)
			}
			out.BlockDevices = append(out.BlockDevices, bd)
		}
	}

	out.Present = out.MDStat.Present || len(out.BlockDevices) > 0 || len(out.MDArrays) > 0 || len(out.DMDevices) > 0
	return out
}

// parseMDStat 解析 /proc/mdstat 的阵列头与状态行。mdstat 是面向人的文本，
// 这里只做保守解析：阵列名、active 状态、[n/m] 磁盘数、同步进度原句。
func parseMDStat(raw string) []mdArray {
	out := []mdArray{}
	var cur *mdArray
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") && strings.Contains(t, ":") {
			name := strings.TrimSpace(t[:strings.Index(t, ":")])
			if !strings.HasPrefix(name, "md") {
				cur = nil
				continue
			}
			fields := strings.Fields(t[strings.Index(t, ":")+1:])
			arr := mdArray{Name: name}
			if len(fields) > 0 {
				arr.State = fields[0]
			}
			for _, f := range fields[1:] {
				if strings.HasPrefix(f, "raid") || f == "linear" || f == "multipath" {
					arr.Level = f
					break
				}
			}
			out = append(out, arr)
			cur = &out[len(out)-1]
			continue
		}
		if cur == nil {
			continue
		}
		if strings.Contains(t, " blocks") && (cur.DisksExpected == nil || cur.DisksActive == nil) {
			if m := mdDisksRe.FindStringSubmatch(t); m != nil {
				if n, err := strconv.Atoi(m[1]); err == nil {
					cur.DisksExpected = ptrInt(n)
				}
				if n, err := strconv.Atoi(m[2]); err == nil {
					cur.DisksActive = ptrInt(n)
				}
			}
		}
		if cur.SyncProgress == "" {
			low := strings.ToLower(t)
			if strings.Contains(low, "=") &&
				(strings.Contains(low, "resync") || strings.Contains(low, "recovery") ||
					strings.Contains(low, "reshape") || strings.Contains(low, "check") ||
					strings.Contains(low, "repair")) {
				cur.SyncProgress = t
			}
		}
	}
	return out
}

// readDirNames 返回目录内条目名（排序、带数量上限）。目录不存在返回空数组。
func readDirNames(dir string, limit int) []string {
	out := []string{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		if len(out) >= limit {
			break
		}
		out = append(out, e.Name())
	}
	return out
}
