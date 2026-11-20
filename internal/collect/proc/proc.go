// Package proc 采集进程表与 top 摘要：首末两次扫描 /proc，把每个进程写为
// top/processes.jsonl 的一行（phase=start/end），并汇总为 top/summary.json。
//
// 约束：只读 /proc，不调用外部命令，不使用第三方依赖；单进程读取失败跳过，
// 整体失败通过 env.AddError("proc", ...) 记录。不读取 /proc/<pid>/environ。
package proc

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
	"luanbird/osdiag/internal/pack"
)

// procPath 是 procfs 挂载点。允许测试替换成临时目录（测试串行执行，无并发写）。
var procPath = "/proc"

const (
	cmdlineMaxBytes = 4 << 10 // cmdline 最多读取 4KiB
	cmdlineArgRunes = 1024    // schema: cmdline item maxLength 1024
	commRunes       = 64      // schema: comm maxLength 64
	pathRunes       = 1024    // schema: exe/cwd/root maxLength 1024
	cgroupRunes     = 1024    // schema: cgroup maxLength 1024
	unitRunes       = 200     // schema: unit maxLength 200
	topCmdlineRunes = 512     // top 摘要里的 cmdline 字符串截断长度

	// 进程 ↔ socket 反查是 best-effort：每次快照最多 readlink 这么多个 fd，
	// 避免在 fd 很多的机器上拖慢采集。
	maxFDPerScan      = 20000
	maxSocketsPerProc = 64
)

// snapshot 是 Begin 快照中 End 需要的最小状态：CPU tick、IO 字节数与快照时间。
type snapshot struct {
	ts    time.Time
	procs map[int]cpuSnap
}

type cpuSnap struct {
	utime   uint64
	stime   uint64
	ioRead  uint64
	ioWrite uint64
}

// snapshots 保存 runID -> *snapshot，供 End 计算 start→end 的 CPU/IO 差。
var snapshots sync.Map

// procSample 是 End 汇总 top 摘要用的紧凑视图，避免把完整记录留在内存里。
type procSample struct {
	PID          int
	PPID         int
	Comm         string
	State        string
	Cmdline      string // 空格连接并截断，用于 top 摘要
	CmdlineEmpty bool   // 内核线程判定用
	RSSBytes     uint64
	Utime        uint64
	Stime        uint64
	IORead       uint64
	IOWrite      uint64
	DelayBlkio   uint64
	Threads      int
	Wchan        string // 仅 D-state 进程读取
}

// Begin 扫描一次进程表，写 phase=start 的 JSONL，并保存 CPU 快照。
// 错误只记录不返回，交由 app 的 runStep 继续后续采集。
func Begin(ctx context.Context, env *collect.Env) error {
	snap := &snapshot{}
	if _, err := scan(ctx, env, "start", snap, false); err != nil {
		env.AddError("proc", "开始快照失败: %v", err)
	}
	snapshots.Store(env.RunID, snap)
	return nil
}

// End 再扫一次进程表，追加 phase=end，计算 counts/top 并写 top/summary.json。
func End(ctx context.Context, env *collect.Env) error {
	var start *snapshot
	if v, ok := snapshots.Load(env.RunID); ok {
		start, _ = v.(*snapshot)
		snapshots.Delete(env.RunID)
	}

	samples, err := scan(ctx, env, "end", nil, true)
	if err != nil {
		env.AddError("proc", "结束快照失败: %v", err)
	}

	listen, lerr := countListening()
	if lerr != nil {
		env.AddError("proc", "统计监听 socket 失败: %v", lerr)
	}

	endTS := time.Now().UTC()
	summary := buildSummary(samples, start, endTS, env.Host.NCPU, listen)

	// 保留其他采集器已写入的 counts，只覆盖进程相关项；即使 summary 写失败
	// 也先把计数交给 manifest，避免整体统计缺项。
	counts := env.Counts()
	counts.Processes = summary.Counts.Processes
	counts.ListeningSockets = summary.Counts.ListeningSockets
	env.SetCounts(counts)

	if err := pack.WriteJSON(env.Path("top/summary.json"), summary); err != nil {
		env.AddError("proc", "写 top/summary.json 失败: %v", err)
	}
	return nil
}

// scan 遍历 /proc/[0-9]+ 并把每个进程写成一行 JSONL。
// snap 非 nil 时填入 CPU 快照；wantSamples 为真时返回汇总用样本。
func scan(ctx context.Context, env *collect.Env, phase string, snap *snapshot, wantSamples bool) ([]procSample, error) {
	ts := time.Now().UTC()
	if snap != nil {
		snap.ts = ts
		snap.procs = make(map[int]cpuSnap)
	}

	path := env.Path("top/processes.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if phase == "start" {
		// 每次运行的开始快照重新建文件，避免 --keep-stage 复用目录时叠加旧数据。
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	entries, err := os.ReadDir(procPath)
	if err != nil {
		return nil, err
	}

	bw := bufio.NewWriterSize(f, 64*1024)
	socketMap := tcpSocketMap()
	fdBudget := maxFDPerScan
	var samples []procSample
	if wantSamples {
		samples = make([]procSample, 0, len(entries))
	}
	tsStr := ts.Format(time.RFC3339)

	for _, ent := range entries {
		if ctx.Err() != nil {
			break
		}
		if !ent.IsDir() {
			continue
		}
		pid, ok := parsePID(ent.Name())
		if !ok {
			continue
		}
		rec, sample, ok := readProcess(pid, phase, tsStr, socketMap, &fdBudget, env.WithEnviron)
		if !ok {
			continue // 进程已退出或不可读，跳过
		}
		b, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		if _, err := bw.Write(b); err != nil {
			return samples, err
		}
		if err := bw.WriteByte('\n'); err != nil {
			return samples, err
		}
		if snap != nil {
			snap.procs[pid] = cpuSnap{
				utime: sample.Utime, stime: sample.Stime,
				ioRead: sample.IORead, ioWrite: sample.IOWrite,
			}
		}
		if wantSamples {
			samples = append(samples, sample)
		}
	}
	if err := bw.Flush(); err != nil {
		return samples, err
	}
	return samples, nil
}

// readProcess 读取单个进程的证据；ok=false 表示 stat 不可用，调用方跳过。
func readProcess(pid int, phase, ts string, sockets map[int64]model.SocketRef, fdBudget *int, withEnviron bool) (model.ProcessRecord, procSample, bool) {
	var rec model.ProcessRecord
	var sample procSample

	statRaw, err := os.ReadFile(procFile(pid, "stat"))
	if err != nil {
		return rec, sample, false
	}
	st, ok := parseStat(string(statRaw))
	if !ok {
		return rec, sample, false
	}

	status := procStatus{}
	if raw, err := os.ReadFile(procFile(pid, "status")); err == nil {
		status = parseStatus(string(raw))
	}

	var args []string
	if raw, err := readLimited(procFile(pid, "cmdline"), cmdlineMaxBytes); err == nil {
		args = splitCmdline(raw)
		for i := range args {
			args[i] = truncateRunes(args[i], cmdlineArgRunes)
		}
	}

	rec = model.ProcessRecord{
		TS:         ts,
		Phase:      phase,
		PID:        pid,
		State:      st.state,
		Comm:       truncateRunes(st.comm, commRunes),
		PPID:       intPtr(st.ppid),
		PGID:       intPtr(st.pgid),
		SID:        intPtr(st.sid),
		UtimeTicks: uint64Ptr(st.utime),
		StimeTicks: uint64Ptr(st.stime),
	}
	if len(args) > 0 {
		rec.Cmdline = args
	}
	rec.StartTicks = st.startTicks
	rec.DelayBlkio = st.delayBlkio
	rec.Exe = readLink(procFile(pid, "exe"))
	rec.Cwd = readLink(procFile(pid, "cwd"))
	rec.Root = readLink(procFile(pid, "root"))
	rec.UID = status.uid
	rec.GID = status.gid
	rec.RSSBytes = status.rssBytes
	rec.VSZBytes = status.vszBytes
	if status.threads != nil {
		rec.Threads = status.threads
	} else if st.threads > 0 {
		rec.Threads = intPtr(st.threads)
	}
	rec.IORead, rec.IOWrite = readProcIO(pid)

	if v, ok := readIntFile(procFile(pid, "oom_score")); ok {
		rec.OOMScore = intPtr(v)
	}
	if v, ok := readIntFile(procFile(pid, "oom_score_adj")); ok {
		rec.OOMAdj = intPtr(v)
	}

	if raw, err := os.ReadFile(procFile(pid, "cgroup")); err == nil {
		cg := parseCgroupPath(string(raw))
		if cg != "" {
			rec.Cgroup = truncateRunes(cg, cgroupRunes)
			rec.Unit = unitFromCgroup(cg)
		}
	}
	if withEnviron {
		rec.Environ = readEnviron(pid)
	}
	rec.NS = readNamespaces(pid)
	if refs := scanProcSockets(pid, sockets, fdBudget); len(refs) > 0 {
		rec.Sockets = refs
	}

	sample = procSample{
		PID:          pid,
		PPID:         st.ppid,
		Comm:         rec.Comm,
		State:        st.state,
		Cmdline:      truncateRunes(strings.Join(args, " "), topCmdlineRunes),
		CmdlineEmpty: len(args) == 0,
		Utime:        st.utime,
		Stime:        st.stime,
		Threads:      intFromPtr(rec.Threads),
	}
	if rec.RSSBytes != nil {
		sample.RSSBytes = *rec.RSSBytes
	}
	if rec.IORead != nil {
		sample.IORead = *rec.IORead
	}
	if rec.IOWrite != nil {
		sample.IOWrite = *rec.IOWrite
	}
	if st.delayBlkio != nil {
		sample.DelayBlkio = *st.delayBlkio
	}
	if st.state == "D" {
		sample.Wchan = readWchan(pid)
	}
	return rec, sample, true
}

func parsePID(name string) (int, bool) {
	pid, err := strconv.Atoi(name)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func procFile(pid int, name string) string {
	return filepath.Join(procPath, strconv.Itoa(pid), name)
}

// readLimited 读取最多 limit 字节；用于 cmdline 的 4KiB 上限。
func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// readEnviron 仅在 --with-environ 时调用；每个进程最多 4KiB，单条变量截断
// 到 1024 字符。内容可能含口令，默认不采集。
func readEnviron(pid int) []string {
	raw, err := readLimited(procFile(pid, "environ"), cmdlineMaxBytes)
	if err != nil || len(raw) == 0 {
		return nil
	}
	parts := strings.Split(string(raw), "\x00")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, truncateRunes(p, cmdlineArgRunes))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func readLink(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return truncateRunes(target, pathRunes)
}

func readIntFile(path string) (int, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, false
	}
	return v, true
}

func readProcIO(pid int) (read, write *uint64) {
	raw, err := os.ReadFile(procFile(pid, "io"))
	if err != nil {
		return nil, nil
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v, err := strconv.ParseUint(strings.TrimSpace(val), 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case "read_bytes":
			read = uint64Ptr(v)
		case "write_bytes":
			write = uint64Ptr(v)
		}
	}
	return read, write
}

func readNamespaces(pid int) *model.Namespaces {
	var ns model.Namespaces
	found := false
	if v, ok := readNSInode(procFile(pid, "ns/pid")); ok {
		ns.PID = uint64Ptr(v)
		found = true
	}
	if v, ok := readNSInode(procFile(pid, "ns/mnt")); ok {
		ns.Mnt = uint64Ptr(v)
		found = true
	}
	if v, ok := readNSInode(procFile(pid, "ns/net")); ok {
		ns.Net = uint64Ptr(v)
		found = true
	}
	if v, ok := readNSInode(procFile(pid, "ns/user")); ok {
		ns.User = uint64Ptr(v)
		found = true
	}
	if !found {
		return nil
	}
	return &ns
}

func readNSInode(path string) (uint64, bool) {
	link, err := os.Readlink(path)
	if err != nil {
		return 0, false
	}
	return parseNSInode(link)
}

func readWchan(pid int) string {
	raw, err := os.ReadFile(procFile(pid, "wchan"))
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(raw))
	if v == "" || v == "0" {
		return ""
	}
	return truncateRunes(v, 128)
}
