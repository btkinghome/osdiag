package proc

import (
	"strconv"
	"strings"
)

// procStat 是 /proc/<pid>/stat 中本采集器关心的字段。
// 字段编号遵循 proc(5)：state=3、utime=14、stime=15、starttime=22、
// delayacct_blkio_ticks=42。
type procStat struct {
	comm       string
	state      string
	ppid       int
	pgid       int
	sid        int
	utime      uint64
	stime      uint64
	threads    int     // 字段 20，0 表示未知
	startTicks *uint64 // 字段 22，自 boot 起的 tick
	delayBlkio *uint64 // 字段 42，内核未开启 delayacct 时可能缺失
}

// parseStat 解析单行 stat。comm 允许包含空格与括号，因此以最后一个 ')' 为界。
// 至少需要到 stime（字段 15）才算可用，否则返回 false。
func parseStat(raw string) (procStat, bool) {
	var st procStat
	raw = strings.TrimRight(raw, "\n")
	openIdx := strings.IndexByte(raw, '(')
	closeIdx := strings.LastIndexByte(raw, ')')
	if openIdx < 0 || closeIdx <= openIdx {
		return st, false
	}
	st.comm = raw[openIdx+1 : closeIdx]
	fields := strings.Fields(raw[closeIdx+1:])
	// fields[0]=state，之后 ppid/pgid/sid...；utime/stime 在 11/12。
	if len(fields) < 13 || fields[0] == "" {
		return st, false
	}
	st.state = fields[0]

	ppid, err := strconv.Atoi(fields[1])
	if err != nil || ppid < 0 {
		return st, false
	}
	st.ppid = ppid
	if v, err := strconv.Atoi(fields[2]); err == nil && v >= 0 {
		st.pgid = v
	}
	if v, err := strconv.Atoi(fields[3]); err == nil && v >= 0 {
		st.sid = v
	}
	utime, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return st, false
	}
	stime, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return st, false
	}
	st.utime, st.stime = utime, stime

	if len(fields) > 17 {
		if v, err := strconv.Atoi(fields[17]); err == nil && v > 0 {
			st.threads = v
		}
	}
	if len(fields) > 19 {
		if v, err := strconv.ParseUint(fields[19], 10, 64); err == nil {
			st.startTicks = uint64Ptr(v)
		}
	}
	if len(fields) > 39 {
		if v, err := strconv.ParseUint(fields[39], 10, 64); err == nil {
			st.delayBlkio = uint64Ptr(v)
		}
	}
	return st, true
}

// procStatus 是 /proc/<pid>/status 中本采集器关心的字段。
type procStatus struct {
	uid      *int
	gid      *int
	rssBytes *uint64
	vszBytes *uint64
	threads  *int
}

// parseStatus 解析 status 文本；缺失或格式异常的字段保持 nil。
func parseStatus(raw string) procStatus {
	var ps procStatus
	for _, line := range strings.Split(raw, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(val)
		if len(fields) == 0 {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Uid":
			if v, err := strconv.Atoi(fields[0]); err == nil && v >= 0 {
				ps.uid = intPtr(v)
			}
		case "Gid":
			if v, err := strconv.Atoi(fields[0]); err == nil && v >= 0 {
				ps.gid = intPtr(v)
			}
		case "VmRSS":
			if v, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
				ps.rssBytes = uint64Ptr(v * 1024)
			}
		case "VmSize":
			if v, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
				ps.vszBytes = uint64Ptr(v * 1024)
			}
		case "Threads":
			if v, err := strconv.Atoi(fields[0]); err == nil && v > 0 {
				ps.threads = intPtr(v)
			}
		}
	}
	return ps
}

// splitCmdline 按 NUL 切分 cmdline，去掉末尾终止符产生的空项。
// 空输入（内核线程或无权限）返回 nil。
func splitCmdline(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	trimmed := strings.TrimRight(string(raw), "\x00")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\x00")
}

// parseCgroupPath 取 /proc/<pid>/cgroup 最后一行的路径段（cgroup v1 多行时取最后
// 一个控制器行，v2 取 0:: 行），例如 "0::/system.slice/app.service" -> "/system.slice/app.service"。
func parseCgroupPath(raw string) string {
	path := ""
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.LastIndexByte(line, ':')
		if i < 0 || i == len(line)-1 {
			continue
		}
		path = strings.TrimSpace(line[i+1:])
	}
	return path
}

// unitFromCgroup 从 cgroup 路径末段提取 systemd unit 名；仅作为标签，不判断服务健康。
func unitFromCgroup(path string) string {
	seg := path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		seg = path[i+1:]
	}
	if strings.HasSuffix(seg, ".service") || strings.HasSuffix(seg, ".scope") {
		return truncateRunes(seg, unitRunes)
	}
	return ""
}

// parseNSInode 从 "pid:[4026531836]" 形式的链接目标提取 inode。
func parseNSInode(link string) (uint64, bool) {
	openIdx := strings.LastIndexByte(link, '[')
	closeIdx := strings.LastIndexByte(link, ']')
	if openIdx < 0 || closeIdx <= openIdx+1 {
		return 0, false
	}
	v, err := strconv.ParseUint(link[openIdx+1:closeIdx], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// truncateRunes 按字符（而非字节）截断，避免切坏 UTF-8。
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func intPtr(v int) *int          { return &v }
func uint64Ptr(v uint64) *uint64 { return &v }

func intFromPtr(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
