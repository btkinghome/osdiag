package metrics

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// cpuTimes 是 /proc/stat 中汇总 cpu 行的时间片（jiffies）。
type cpuTimes struct {
	user      uint64
	nice      uint64
	system    uint64
	idle      uint64
	iowait    uint64
	irq       uint64
	softirq   uint64
	steal     uint64
	guest     uint64
	guestNice uint64
}

// total 返回计算占用率所需的总时间片。guest 已经包含在 user/nice 中，
// 因此这里不再重复计入，避免分母虚高。
func (c cpuTimes) total() uint64 {
	return c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal
}

type procStatSnapshot struct {
	cpu          cpuTimes
	ctxt         uint64
	procsRunning int
	procsBlocked int
}

// parseProcStat 解析 /proc/stat 文本，提取汇总 cpu 行、ctxt 与 procs 行。
func parseProcStat(s string) (procStatSnapshot, error) {
	var out procStatSnapshot
	foundCPU := false
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "cpu":
			if foundCPU {
				continue
			}
			if len(fields) < 8 {
				return out, fmt.Errorf("/proc/stat 汇总 cpu 行字段不足: %q", line)
			}
			vals := make([]uint64, len(fields)-1)
			for i, raw := range fields[1:] {
				v, err := strconv.ParseUint(raw, 10, 64)
				if err != nil {
					return out, fmt.Errorf("解析 /proc/stat cpu 字段 %q: %w", raw, err)
				}
				vals[i] = v
			}
			out.cpu = cpuTimes{
				user:    vals[0],
				nice:    vals[1],
				system:  vals[2],
				idle:    vals[3],
				iowait:  vals[4],
				irq:     vals[5],
				softirq: vals[6],
				steal:   vals[7],
			}
			if len(vals) > 8 {
				out.cpu.guest = vals[8]
			}
			if len(vals) > 9 {
				out.cpu.guestNice = vals[9]
			}
			foundCPU = true
		case "ctxt":
			if len(fields) >= 2 {
				if v, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
					out.ctxt = v
				}
			}
		case "procs_running":
			if len(fields) >= 2 {
				if v, err := strconv.Atoi(fields[1]); err == nil {
					out.procsRunning = v
				}
			}
		case "procs_blocked":
			if len(fields) >= 2 {
				if v, err := strconv.Atoi(fields[1]); err == nil {
					out.procsBlocked = v
				}
			}
		}
	}
	if !foundCPU {
		return out, fmt.Errorf("/proc/stat 缺少汇总 cpu 行")
	}
	return out, nil
}

type loadAvg struct {
	one     float64
	five    float64
	fifteen float64
}

// parseLoadavg 解析 /proc/loadavg 的前三个负载值。
func parseLoadavg(s string) (loadAvg, error) {
	fields := strings.Fields(s)
	if len(fields) < 3 {
		return loadAvg{}, fmt.Errorf("/proc/loadavg 字段不足: %q", s)
	}
	var out loadAvg
	var err error
	if out.one, err = strconv.ParseFloat(fields[0], 64); err != nil {
		return out, fmt.Errorf("解析 load1: %w", err)
	}
	if out.five, err = strconv.ParseFloat(fields[1], 64); err != nil {
		return out, fmt.Errorf("解析 load5: %w", err)
	}
	if out.fifteen, err = strconv.ParseFloat(fields[2], 64); err != nil {
		return out, fmt.Errorf("解析 load15: %w", err)
	}
	return out, nil
}

// parseMeminfo 解析 /proc/meminfo，返回字节数（带 kB 单位的字段乘以 1024，
// 无单位字段如 HugePages_Total 保持原值）。
func parseMeminfo(s string) map[string]uint64 {
	out := make(map[string]uint64, 64)
	for _, line := range strings.Split(s, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if len(fields) >= 2 && strings.EqualFold(fields[1], "kB") {
			v *= 1024
		}
		out[strings.TrimSpace(key)] = v
	}
	return out
}

type pressureSample struct {
	avg10  float64
	avg60  float64
	avg300 float64
	total  uint64
}

// parsePressure 解析 /proc/pressure/<cpu|io|memory>，返回 some/full 两组均值。
func parsePressure(s string) map[string]pressureSample {
	out := make(map[string]pressureSample, 2)
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kind := fields[0]
		var ps pressureSample
		for _, f := range fields[1:] {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			switch k {
			case "avg10":
				ps.avg10, _ = strconv.ParseFloat(v, 64)
			case "avg60":
				ps.avg60, _ = strconv.ParseFloat(v, 64)
			case "avg300":
				ps.avg300, _ = strconv.ParseFloat(v, 64)
			case "total":
				ps.total, _ = strconv.ParseUint(v, 10, 64)
			}
		}
		out[kind] = ps
	}
	return out
}

// diskStat 是块设备累计计数器。单位：IO 次数、512B 扇区、毫秒。
type diskStat struct {
	name         string
	readIOs      uint64
	readMerges   uint64
	readSectors  uint64
	readTicks    uint64
	writeIOs     uint64
	writeMerges  uint64
	writeSectors uint64
	writeTicks   uint64
	inFlight     uint64
	ioTicks      uint64
	timeInQueue  uint64
}

func isUint(s string) bool {
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

// parseDiskStat 解析块设备 stat 行，兼容 /sys/block/<dev>/stat（无前缀）
// 与 /proc/diskstats（"major minor name" 前缀）两种格式。
func parseDiskStat(s string) (diskStat, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return diskStat{}, fmt.Errorf("空的块设备 stat 行")
	}
	var out diskStat
	valueStart := 0
	// /proc/diskstats 为 "major minor name ..."；第三个字段是设备名（非数字）。
	// 仅凭前两个数字字段会误判将来新增了字段的 sysfs 格式，故同时要求第三个
	// 字段不是数字。
	if len(fields) >= 14 && isUint(fields[0]) && isUint(fields[1]) && !isUint(fields[2]) {
		out.name = fields[2]
		valueStart = 3
	}
	if len(fields) < valueStart+11 {
		return out, fmt.Errorf("块设备 stat 字段不足: %q", s)
	}
	vals := make([]uint64, 11)
	for i := 0; i < 11; i++ {
		v, err := strconv.ParseUint(fields[valueStart+i], 10, 64)
		if err != nil {
			return out, fmt.Errorf("解析块设备 stat 第 %d 个字段 %q: %w", i+1, fields[valueStart+i], err)
		}
		vals[i] = v
	}
	out.readIOs = vals[0]
	out.readMerges = vals[1]
	out.readSectors = vals[2]
	out.readTicks = vals[3]
	out.writeIOs = vals[4]
	out.writeMerges = vals[5]
	out.writeSectors = vals[6]
	out.writeTicks = vals[7]
	out.inFlight = vals[8]
	out.ioTicks = vals[9]
	out.timeInQueue = vals[10]
	return out, nil
}

type mountEntry struct {
	device     string
	mountpoint string
	fstype     string
	options    []string
}

// unescapeMount 还原挂载字段中的八进制转义（如 \040 空格）。
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	return strings.NewReplacer(
		`\040`, " ",
		`\011`, "\t",
		`\012`, "\n",
		`\134`, `\`,
	).Replace(s)
}

// parseMounts 解析 /proc/self/mounts（与 /proc/mounts 同格式）。
func parseMounts(s string) []mountEntry {
	var out []mountEntry
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		out = append(out, mountEntry{
			device:     unescapeMount(fields[0]),
			mountpoint: unescapeMount(fields[1]),
			fstype:     fields[2],
			options:    strings.Split(fields[3], ","),
		})
	}
	return out
}

var pseudoFilesystems = map[string]bool{
	"proc":         true,
	"procfs":       true,
	"sysfs":        true,
	"devtmpfs":     true,
	"devpts":       true,
	"tmpfs":        true,
	"ramfs":        true,
	"cgroup":       true,
	"cgroup2":      true,
	"debugfs":      true,
	"securityfs":   true,
	"pstore":       true,
	"bpf":          true,
	"tracefs":      true,
	"configfs":     true,
	"fusectl":      true,
	"mqueue":       true,
	"hugetlbfs":    true,
	"autofs":       true,
	"binfmt_misc":  true,
	"rpc_pipefs":   true,
	"nsfs":         true,
	"selinuxfs":    true,
	"efivarfs":     true,
	"fuse.lxcfs":   true,
	"fuse.gvfsd":   true,
	"fuse.portal":  true,
	"fuse.sshfs":   true,
	"fuse.rclone":  true,
	"fuse.s3fs":    true,
	"fuse.gluster": true,
}

// networkFilesystems 的 statfs 可能在内核态阻塞（D 状态），默认跳过。
func isNetworkFilesystem(fstype string) bool {
	switch fstype {
	case "nfs", "nfs4", "cifs", "smb3", "smbfs", "ceph", "glusterfs", "9p", "afs", "ncpfs":
		return true
	}
	return strings.HasPrefix(fstype, "fuse")
}

// shouldSkipMount 返回是否跳过该挂载点及中文原因。
func shouldSkipMount(e mountEntry) (bool, string) {
	if e.mountpoint == "" || e.device == "" {
		return true, "无效挂载项"
	}
	if pseudoFilesystems[strings.ToLower(e.fstype)] {
		return true, "伪文件系统"
	}
	if isNetworkFilesystem(strings.ToLower(e.fstype)) {
		return true, "网络文件系统默认跳过 statfs，避免 D 状态阻塞"
	}
	return false, ""
}

func mountReadOnly(e mountEntry) bool {
	for _, opt := range e.options {
		if opt == "ro" {
			return true
		}
	}
	return false
}

type conntrackCounters struct {
	insertFailed  uint64
	drop          uint64
	earlyDrop     uint64
	searchRestart uint64
}

// parseConntrackStat 解析 /proc/net/stat/nf_conntrack，按表头求和各 CPU 的
// 十六进制计数列。
func parseConntrackStat(s string) (conntrackCounters, error) {
	lines := strings.Split(s, "\n")
	var header []string
	for _, line := range lines {
		if f := strings.Fields(line); len(f) > 0 {
			header = f
			break
		}
	}
	if len(header) == 0 {
		return conntrackCounters{}, fmt.Errorf("空的 nf_conntrack 统计")
	}
	idx := make(map[string]int, len(header))
	for i, name := range header {
		idx[name] = i
	}
	var out conntrackCounters
	sum := func(name string, dst *uint64) {
		i, ok := idx[name]
		if !ok {
			return
		}
		for _, line := range lines[1:] {
			fields := strings.Fields(line)
			if len(fields) <= i {
				continue
			}
			if v, err := strconv.ParseUint(fields[i], 16, 64); err == nil {
				*dst += v
			}
		}
	}
	sum("insert_failed", &out.insertFailed)
	sum("drop", &out.drop)
	sum("early_drop", &out.earlyDrop)
	sum("search_restart", &out.searchRestart)
	return out, nil
}

// counterDelta 处理计数器回绕/重置：负差值按 0 计。
func counterDelta(cur, prev uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}

func roundTo(v float64, digits int) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	pow := math.Pow10(digits)
	return math.Round(v*pow) / pow
}
