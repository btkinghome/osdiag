package metrics

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// 采集来源。集中定义便于审阅；测试只覆盖纯解析函数。
const (
	procStatPath       = "/proc/stat"
	procLoadavgPath    = "/proc/loadavg"
	procMeminfoPath    = "/proc/meminfo"
	procMountsPath     = "/proc/self/mounts"
	sysBlockDir        = "/sys/block"
	sysNetDir          = "/sys/class/net"
	procPressureDir    = "/proc/pressure"
	conntrackCountPath = "/proc/sys/net/netfilter/nf_conntrack_count"
	conntrackMaxPath   = "/proc/sys/net/netfilter/nf_conntrack_max"
	conntrackStatPath  = "/proc/net/stat/nf_conntrack"
)

func readUintFile(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return 0, fmt.Errorf("空文件: %s", path)
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("解析 %s: %w", path, err)
	}
	return v, nil
}

func readIntFile(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return 0, fmt.Errorf("空文件: %s", path)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("解析 %s: %w", path, err)
	}
	return v, nil
}

func readUintOrZero(path string) uint64 {
	v, err := readUintFile(path)
	if err != nil {
		return 0
	}
	return v
}

func readProcStatSnapshot() (procStatSnapshot, error) {
	b, err := os.ReadFile(procStatPath)
	if err != nil {
		return procStatSnapshot{}, err
	}
	return parseProcStat(string(b))
}

func readLoadAvg() (loadAvg, error) {
	b, err := os.ReadFile(procLoadavgPath)
	if err != nil {
		return loadAvg{}, err
	}
	return parseLoadavg(string(b))
}

// ---------------------------------------------------------------- cpu

type cpuCollector struct {
	prev    procStatSnapshot
	have    bool
	sampled bool
}

func (c *cpuCollector) setup() (int, error) {
	st, err := readProcStatSnapshot()
	if err != nil {
		return 0, err
	}
	c.prev = st
	c.have = true
	return 1, nil
}

type cpuPct struct {
	user   float64
	system float64
	iowait float64
	idle   float64
	steal  float64
}

// cpuPercentages 用两次采样的差值计算各状态占比。nice 计入 user，
// irq/softirq 计入 system，guest 已包含在 user/nice 中不重复计算。
func cpuPercentages(cur, prev cpuTimes) cpuPct {
	du := counterDelta(cur.user, prev.user)
	dn := counterDelta(cur.nice, prev.nice)
	ds := counterDelta(cur.system, prev.system)
	di := counterDelta(cur.idle, prev.idle)
	dw := counterDelta(cur.iowait, prev.iowait)
	dirq := counterDelta(cur.irq, prev.irq)
	dsoft := counterDelta(cur.softirq, prev.softirq)
	dst := counterDelta(cur.steal, prev.steal)
	total := du + dn + ds + di + dw + dirq + dsoft + dst
	if total == 0 {
		return cpuPct{}
	}
	return cpuPct{
		user:   float64(du+dn) / float64(total) * 100,
		system: float64(ds+dirq+dsoft) / float64(total) * 100,
		iowait: float64(dw) / float64(total) * 100,
		idle:   float64(di) / float64(total) * 100,
		steal:  float64(dst) / float64(total) * 100,
	}
}

func (c *cpuCollector) sample(time.Time) ([]row, error) {
	cur, err := readProcStatSnapshot()
	if err != nil {
		return nil, err
	}
	var errs []error
	la, laErr := readLoadAvg()
	if laErr != nil {
		errs = append(errs, laErr)
	}

	pct := cpuPct{}
	if c.sampled && c.have {
		pct = cpuPercentages(cur.cpu, c.prev.cpu)
	}

	r := row{
		"cpu":           "all",
		"user_pct":      roundTo(pct.user, 2),
		"system_pct":    roundTo(pct.system, 2),
		"iowait_pct":    roundTo(pct.iowait, 2),
		"idle_pct":      roundTo(pct.idle, 2),
		"steal_pct":     roundTo(pct.steal, 2),
		"load1":         roundTo(la.one, 2),
		"load5":         roundTo(la.five, 2),
		"load15":        roundTo(la.fifteen, 2),
		"procs_running": cur.procsRunning,
		"procs_blocked": cur.procsBlocked,
		"ctx_switches":  cur.ctxt,
	}

	c.prev = cur
	c.have = true
	c.sampled = true
	return []row{r}, errors.Join(errs...)
}

// ---------------------------------------------------------------- memory

type memoryCollector struct{}

func (c *memoryCollector) setup() (int, error) {
	b, err := os.ReadFile(procMeminfoPath)
	if err != nil {
		return 0, err
	}
	if len(parseMeminfo(string(b))) == 0 {
		return 0, fmt.Errorf("/proc/meminfo 无可解析字段")
	}
	return 1, nil
}

func memValue(m map[string]uint64, keys ...string) uint64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return 0
}

func (c *memoryCollector) sample(time.Time) ([]row, error) {
	b, err := os.ReadFile(procMeminfoPath)
	if err != nil {
		return nil, err
	}
	m := parseMeminfo(string(b))
	avail := memValue(m, "MemAvailable")
	if avail == 0 {
		avail = memValue(m, "MemFree") + memValue(m, "Buffers") + memValue(m, "Cached")
	}
	r := row{
		"mem_total_bytes":     memValue(m, "MemTotal"),
		"mem_available_bytes": avail,
		"mem_free_bytes":      memValue(m, "MemFree"),
		"buffers_bytes":       memValue(m, "Buffers"),
		"cached_bytes":        memValue(m, "Cached"),
		"swap_total_bytes":    memValue(m, "SwapTotal"),
		"swap_free_bytes":     memValue(m, "SwapFree"),
		"dirty_bytes":         memValue(m, "Dirty"),
		"writeback_bytes":     memValue(m, "Writeback"),
		"anon_bytes":          memValue(m, "AnonPages"),
		"slab_bytes":          memValue(m, "Slab"),
		"hugepages_total":     memValue(m, "HugePages_Total"),
		"hugepages_free":      memValue(m, "HugePages_Free"),
	}
	return []row{r}, nil
}

// ---------------------------------------------------------------- pressure

type pressureCollector struct{}

func (c *pressureCollector) setup() (int, error) {
	for _, name := range []string{"cpu", "io", "memory"} {
		if _, err := os.Stat(filepath.Join(procPressureDir, name)); err == nil {
			return 1, nil
		}
	}
	return 0, &unsupportedError{note: "当前内核无 /proc/pressure（PSI 需 Linux 4.20+ 且 CONFIG_PSI=y；本机内核 4.19 未提供），已登记空流。"}
}

func (c *pressureCollector) sample(time.Time) ([]row, error) {
	var errs []error
	byKind := make(map[string]map[string]pressureSample, 3)
	for _, name := range []string{"cpu", "io", "memory"} {
		b, err := os.ReadFile(filepath.Join(procPressureDir, name))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		byKind[name] = parsePressure(string(b))
	}
	some := func(kind string) pressureSample { return byKind[kind]["some"] }
	full := func(kind string) pressureSample { return byKind[kind]["full"] }

	sc := some("cpu")
	si := some("io")
	sm := some("memory")
	fi := full("io")
	r := row{
		"some_cpu_avg10":     roundTo(sc.avg10, 2),
		"some_cpu_avg60":     roundTo(sc.avg60, 2),
		"some_cpu_avg300":    roundTo(sc.avg300, 2),
		"some_io_avg10":      roundTo(si.avg10, 2),
		"some_io_avg60":      roundTo(si.avg60, 2),
		"some_io_avg300":     roundTo(si.avg300, 2),
		"some_memory_avg10":  roundTo(sm.avg10, 2),
		"some_memory_avg60":  roundTo(sm.avg60, 2),
		"some_memory_avg300": roundTo(sm.avg300, 2),
		"full_io_avg10":      roundTo(fi.avg10, 2),
		"full_io_avg60":      roundTo(fi.avg60, 2),
		"full_io_avg300":     roundTo(fi.avg300, 2),
	}
	return []row{r}, errors.Join(errs...)
}

// ---------------------------------------------------------------- disk

func readBlockStat(name string) (diskStat, error) {
	b, err := os.ReadFile(filepath.Join(sysBlockDir, name, "stat"))
	if err != nil {
		return diskStat{}, err
	}
	return parseDiskStat(string(b))
}

type diskCollector struct {
	interval time.Duration
	order    []string
	prev     map[string]diskStat
	prevAt   map[string]time.Time
	sampled  bool
}

func (c *diskCollector) setup() (int, error) {
	entries, err := os.ReadDir(sysBlockDir)
	if err != nil {
		return 0, err
	}
	c.prev = make(map[string]diskStat)
	c.prevAt = make(map[string]time.Time)
	for _, e := range entries {
		if st, err := os.Stat(filepath.Join(sysBlockDir, e.Name())); err != nil || !st.IsDir() {
			continue
		}
		st, err := readBlockStat(e.Name())
		if err != nil {
			continue
		}
		c.order = append(c.order, e.Name())
		c.prev[e.Name()] = st
		c.prevAt[e.Name()] = time.Now()
	}
	sort.Strings(c.order)
	if len(c.order) == 0 {
		return 0, fmt.Errorf("%s 下没有可读的块设备 stat", sysBlockDir)
	}
	return len(c.order), nil
}

func (c *diskCollector) sample(now time.Time) ([]row, error) {
	var errs []error
	rows := make([]row, 0, len(c.order))
	for _, name := range c.order {
		st, err := readBlockStat(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		prev, hasPrev := c.prev[name]
		var readIOPS, writeIOPS, readBps, writeBps, awaitMS, utilPct float64
		if c.sampled && hasPrev {
			dt := now.Sub(c.prevAt[name]).Seconds()
			if dt <= 0 {
				dt = c.interval.Seconds()
			}
			dReadIO := counterDelta(st.readIOs, prev.readIOs)
			dWriteIO := counterDelta(st.writeIOs, prev.writeIOs)
			dReadBytes := counterDelta(st.readSectors, prev.readSectors) * 512
			dWriteBytes := counterDelta(st.writeSectors, prev.writeSectors) * 512
			dReadTicks := counterDelta(st.readTicks, prev.readTicks)
			dWriteTicks := counterDelta(st.writeTicks, prev.writeTicks)
			dIOTicks := counterDelta(st.ioTicks, prev.ioTicks)

			readIOPS = float64(dReadIO) / dt
			writeIOPS = float64(dWriteIO) / dt
			readBps = float64(dReadBytes) / dt
			writeBps = float64(dWriteBytes) / dt
			if dOps := dReadIO + dWriteIO; dOps > 0 {
				awaitMS = float64(dReadTicks+dWriteTicks) / float64(dOps)
			}
			utilPct = float64(dIOTicks) / (dt * 1000) * 100
			if utilPct > 100 {
				utilPct = 100
			}
		}
		rows = append(rows, row{
			"device":            name,
			"read_iops":         roundTo(readIOPS, 2),
			"write_iops":        roundTo(writeIOPS, 2),
			"read_bytes_per_s":  roundTo(readBps, 0),
			"write_bytes_per_s": roundTo(writeBps, 0),
			"write_bytes_total": st.writeSectors * 512,
			"await_ms":          roundTo(awaitMS, 2),
			"util_pct":          roundTo(utilPct, 2),
			"inflight":          st.inFlight,
		})
		c.prev[name] = st
		c.prevAt[name] = now
	}
	c.sampled = true
	return rows, errors.Join(errs...)
}

// ---------------------------------------------------------------- filesystem

type fsCollector struct {
	entries    []mountEntry
	skippedNum int
}

func (c *fsCollector) setup() (int, error) {
	b, err := os.ReadFile(procMountsPath)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]bool)
	for _, e := range parseMounts(string(b)) {
		if skip, _ := shouldSkipMount(e); skip {
			c.skippedNum++
			continue
		}
		if seen[e.mountpoint] {
			continue
		}
		seen[e.mountpoint] = true
		c.entries = append(c.entries, e)
	}
	sort.Slice(c.entries, func(i, j int) bool { return c.entries[i].mountpoint < c.entries[j].mountpoint })
	if len(c.entries) == 0 {
		return 0, &unsupportedError{note: "没有可 statfs 的本地文件系统挂载（伪文件系统与网络文件系统已跳过）。"}
	}
	return len(c.entries), nil
}

func (c *fsCollector) sample(time.Time) ([]row, error) {
	var errs []error
	rows := make([]row, 0, len(c.entries))
	for _, e := range c.entries {
		var st syscall.Statfs_t
		if err := syscall.Statfs(e.mountpoint, &st); err != nil {
			errs = append(errs, fmt.Errorf("statfs %s: %w", e.mountpoint, err))
			continue
		}
		bsize := uint64(st.Bsize)
		total := st.Blocks * bsize
		free := st.Bfree * bsize
		used := uint64(0)
		if total >= free {
			used = total - free
		}
		rows = append(rows, row{
			"mount":        e.mountpoint,
			"device":       e.device,
			"fstype":       e.fstype,
			"size_bytes":   total,
			"used_bytes":   used,
			"avail_bytes":  st.Bavail * bsize,
			"inodes_total": st.Files,
			"inodes_free":  st.Ffree,
			"read_only":    mountReadOnly(e),
		})
	}
	return rows, errors.Join(errs...)
}

// ---------------------------------------------------------------- network

type netCollector struct {
	ifaces []string
}

func (c *netCollector) setup() (int, error) {
	entries, err := os.ReadDir(sysNetDir)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		// /sys/class/net/<iface> 是符号链接，必须 Stat 跟随后再判断目录。
		if st, err := os.Stat(filepath.Join(sysNetDir, e.Name())); err == nil && st.IsDir() {
			c.ifaces = append(c.ifaces, e.Name())
		}
	}
	sort.Strings(c.ifaces)
	if len(c.ifaces) == 0 {
		return 0, fmt.Errorf("%s 下没有网络接口", sysNetDir)
	}
	return len(c.ifaces), nil
}

var netCounterFiles = []string{
	"rx_bytes", "tx_bytes", "rx_packets", "tx_packets",
	"rx_errors", "tx_errors", "rx_dropped", "tx_dropped",
}

func (c *netCollector) sample(time.Time) ([]row, error) {
	var errs []error
	rows := make([]row, 0, len(c.ifaces))
	for _, iface := range c.ifaces {
		base := filepath.Join(sysNetDir, iface)
		if _, err := os.Stat(base); err != nil {
			errs = append(errs, err)
			continue
		}
		vals := make([]uint64, len(netCounterFiles))
		for i, name := range netCounterFiles {
			vals[i] = readUintOrZero(filepath.Join(base, "statistics", name))
		}
		speed := 0
		if v, err := readIntFile(filepath.Join(base, "speed")); err == nil && v > 0 {
			speed = int(v)
		}
		carrier := false
		if v, err := readUintFile(filepath.Join(base, "carrier")); err == nil {
			carrier = v != 0
		}
		operstate := "unknown"
		if b, err := os.ReadFile(filepath.Join(base, "operstate")); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				operstate = s
			}
		}
		rows = append(rows, row{
			"iface":      iface,
			"rx_bytes":   vals[0],
			"tx_bytes":   vals[1],
			"rx_packets": vals[2],
			"tx_packets": vals[3],
			"rx_errors":  vals[4],
			"tx_errors":  vals[5],
			"rx_dropped": vals[6],
			"tx_dropped": vals[7],
			"speed_mbps": speed,
			"carrier":    carrier,
			"operstate":  operstate,
		})
	}
	return rows, errors.Join(errs...)
}

// ---------------------------------------------------------------- conntrack

type conntrackCollector struct{}

func (c *conntrackCollector) setup() (int, error) {
	if _, err := os.ReadFile(conntrackCountPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, errSkipStream
		}
		return 0, err
	}
	return 1, nil
}

func readConntrackCounters() conntrackCounters {
	if b, err := os.ReadFile(conntrackStatPath); err == nil {
		if c, err := parseConntrackStat(string(b)); err == nil {
			return c
		}
	}
	var c conntrackCounters
	c.insertFailed = readUintOrZero("/proc/sys/net/netfilter/nf_conntrack_insert_failed")
	c.drop = readUintOrZero("/proc/sys/net/netfilter/nf_conntrack_drop")
	c.earlyDrop = readUintOrZero("/proc/sys/net/netfilter/nf_conntrack_early_drop")
	c.searchRestart = readUintOrZero("/proc/sys/net/netfilter/nf_conntrack_search_restart")
	return c
}

func (c *conntrackCollector) sample(time.Time) ([]row, error) {
	var errs []error
	entries, err := readUintFile(conntrackCountPath)
	if err != nil {
		errs = append(errs, err)
	}
	maxEntries, err := readUintFile(conntrackMaxPath)
	if err != nil {
		errs = append(errs, err)
	}
	ctr := readConntrackCounters()
	r := row{
		"entries":        entries,
		"max":            maxEntries,
		"insert_failed":  ctr.insertFailed,
		"drop":           ctr.drop,
		"early_drop":     ctr.earlyDrop,
		"search_restart": ctr.searchRestart,
	}
	return []row{r}, errors.Join(errs...)
}
