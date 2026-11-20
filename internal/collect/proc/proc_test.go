package proc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
)

// statFixture 生成一行 stat；n 是 comm 之后的字段数（以 state 为第 0 个）。
func statFixture(pid int, comm, state string, utime, stime, start, delay uint64, n int) string {
	fields := make([]string, n)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = state
	fields[1] = "1"
	fields[2] = "2"
	fields[3] = "3"
	if n > 11 {
		fields[11] = strconv.FormatUint(utime, 10)
	}
	if n > 12 {
		fields[12] = strconv.FormatUint(stime, 10)
	}
	if n > 17 {
		fields[17] = "7"
	}
	if n > 19 {
		fields[19] = strconv.FormatUint(start, 10)
	}
	if n > 39 {
		fields[39] = strconv.FormatUint(delay, 10)
	}
	return fmt.Sprintf("%d (%s) %s", pid, comm, strings.Join(fields, " "))
}

func TestParseStat(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want procStat
		ok   bool
	}{
		{
			name: "basic",
			raw:  statFixture(2145, "java", "S", 9182340, 412300, 130422, 45210, 40) + "\n",
			want: procStat{
				comm: "java", state: "S", ppid: 1, pgid: 2, sid: 3,
				utime: 9182340, stime: 412300, threads: 7,
				startTicks: uint64Ptr(130422), delayBlkio: uint64Ptr(45210),
			},
			ok: true,
		},
		{
			name: "comm with spaces and parens",
			raw:  statFixture(77, "a) b (c", "R", 10, 20, 30, 0, 40),
			want: procStat{
				comm: "a) b (c", state: "R", ppid: 1, pgid: 2, sid: 3,
				utime: 10, stime: 20, threads: 7,
				startTicks: uint64Ptr(30), delayBlkio: uint64Ptr(0),
			},
			ok: true,
		},
		{
			name: "no delayacct fields",
			raw:  statFixture(9, "kworker/3:1", "I", 5, 6, 7, 0, 20),
			want: procStat{
				comm: "kworker/3:1", state: "I", ppid: 1, pgid: 2, sid: 3,
				utime: 5, stime: 6, threads: 7, startTicks: uint64Ptr(7),
			},
			ok: true,
		},
		{
			name: "only through stime",
			raw:  statFixture(9, "init", "S", 5, 6, 0, 0, 13),
			want: procStat{comm: "init", state: "S", ppid: 1, pgid: 2, sid: 3, utime: 5, stime: 6},
			ok:   true,
		},
		{
			name: "too short",
			raw:  statFixture(9, "init", "S", 5, 6, 0, 0, 12),
			ok:   false,
		},
		{
			name: "missing parens",
			raw:  "123 S 1 2 3",
			ok:   false,
		},
		{
			name: "empty line",
			raw:  "",
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseStat(tt.raw)
			if ok != tt.ok {
				t.Fatalf("parseStat ok=%v, want %v (got=%+v)", ok, tt.ok, got)
			}
			if !ok {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseStat got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want procStatus
	}{
		{
			name: "full",
			raw: "Name:\tjava\n" +
				"Uid:\t1001\t1002\t1001\t1001\n" +
				"Gid:\t1001\t2002\t1001\t1001\n" +
				"VmSize:\t 1048576 kB\n" +
				"VmRSS:\t 2048 kB\n" +
				"Threads:\t214\n",
			want: procStatus{
				uid: intPtr(1001), gid: intPtr(1001),
				rssBytes: uint64Ptr(2048 * 1024), vszBytes: uint64Ptr(1048576 * 1024),
				threads: intPtr(214),
			},
		},
		{
			name: "missing fields",
			raw:  "Name:\tfoo\nState:\tS (sleeping)\n",
			want: procStatus{},
		},
		{
			name: "malformed values",
			raw:  "Uid:\tabc\nGid:\t-1\nVmRSS:\txx kB\nThreads:\t0\n",
			want: procStatus{},
		},
		{
			name: "empty",
			raw:  "",
			want: procStatus{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseStatus(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseStatus got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSplitCmdline(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "nil", raw: "", want: nil},
		{name: "only nul terminator", raw: "\x00", want: nil},
		{name: "typical", raw: "bash\x00-l\x00", want: []string{"bash", "-l"}},
		{name: "spaces in arg", raw: "/opt/my app/run\x00--x=1 2\x00", want: []string{"/opt/my app/run", "--x=1 2"}},
		{name: "double trailing nul", raw: "a b\x00c\x00\x00", want: []string{"a b", "c"}},
		{name: "empty arg in middle", raw: "/x\x00\x00y\x00", want: []string{"/x", "", "y"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitCmdline([]byte(tt.raw))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("splitCmdline(%q) got %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseCgroupPath(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantPath string
		wantUnit string
	}{
		{name: "v2 service", raw: "0::/system.slice/app.service\n", wantPath: "/system.slice/app.service", wantUnit: "app.service"},
		{name: "v1 last line", raw: "5:cpu:/user.slice\n1:name=systemd:/system.slice/nginx.service\n", wantPath: "/system.slice/nginx.service", wantUnit: "nginx.service"},
		{name: "root", raw: "0::/\n", wantPath: "/"},
		{name: "scope", raw: "0::/user.slice/user-1000.slice/session-1.scope\n", wantPath: "/user.slice/user-1000.slice/session-1.scope", wantUnit: "session-1.scope"},
		{name: "not a unit", raw: "0::/system.slice\n", wantPath: "/system.slice"},
		{name: "empty", raw: "", wantPath: ""},
		{name: "no colon", raw: "garbage\n", wantPath: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := parseCgroupPath(tt.raw)
			if path != tt.wantPath {
				t.Fatalf("parseCgroupPath = %q, want %q", path, tt.wantPath)
			}
			if unit := unitFromCgroup(path); unit != tt.wantUnit {
				t.Fatalf("unitFromCgroup(%q) = %q, want %q", path, unit, tt.wantUnit)
			}
		})
	}
}

func TestParseNSInode(t *testing.T) {
	tests := []struct {
		link string
		want uint64
		ok   bool
	}{
		{"pid:[4026531836]", 4026531836, true},
		{"mnt:[4026531840]", 4026531840, true},
		{"pid:[abc]", 0, false},
		{"pid:[]", 0, false},
		{"net:", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.link, func(t *testing.T) {
			got, ok := parseNSInode(tt.link)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("parseNSInode(%q) = (%d,%v), want (%d,%v)", tt.link, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestFormatEndpoint(t *testing.T) {
	tests := []struct {
		addr string
		v6   bool
		want string
	}{
		{"0100007F:1F90", false, "127.0.0.1:8080"},
		{"00000000:0050", false, "0.0.0.0:80"},
		{"00000000000000000000000001000000:0050", true, "[::1]:80"},
		{"00000000000000000000000000000000:0016", true, "[::]:22"},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := formatEndpoint(tt.addr, tt.v6); got != tt.want {
				t.Fatalf("formatEndpoint(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestParseSocketLink(t *testing.T) {
	tests := []struct {
		link string
		want int64
		ok   bool
	}{
		{"socket:[38411]", 38411, true},
		{"socket:[0]", 0, false},
		{"pipe:[123]", 0, false},
		{"/dev/null", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.link, func(t *testing.T) {
			got, ok := parseSocketLink(tt.link)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("parseSocketLink(%q) = (%d,%v), want (%d,%v)", tt.link, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestIODelta(t *testing.T) {
	prev := cpuSnap{ioRead: 1000, ioWrite: 500}
	tests := []struct {
		name             string
		end              procSample
		wantRead, wantWr uint64
		ok               bool
	}{
		{name: "normal", end: procSample{IORead: 3000, IOWrite: 1500}, wantRead: 2000, wantWr: 1000, ok: true},
		{name: "write only", end: procSample{IORead: 1000, IOWrite: 900}, wantRead: 0, wantWr: 400, ok: true},
		{name: "no delta", end: procSample{IORead: 1000, IOWrite: 500}, ok: false},
		{name: "read reset", end: procSample{IORead: 10, IOWrite: 500}, ok: false},
		{name: "write reset", end: procSample{IORead: 1000, IOWrite: 0}, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read, write, ok := ioDelta(prev, tt.end)
			if ok != tt.ok || read != tt.wantRead || write != tt.wantWr {
				t.Fatalf("ioDelta = (%d,%d,%v), want (%d,%d,%v)", read, write, ok, tt.wantRead, tt.wantWr, tt.ok)
			}
		})
	}
}

func TestBuildSummary(t *testing.T) {
	startTS := time.Date(2026, 11, 20, 7, 0, 0, 0, time.UTC)
	endTS := startTS.Add(2 * time.Second)
	start := &snapshot{
		ts: startTS,
		procs: map[int]cpuSnap{
			101: {utime: 100, stime: 50, ioRead: 1000, ioWrite: 500}, // 正常：delta=150 ticks -> 1500ms
			102: {utime: 1000, stime: 0},                             // 超出 2s×4 核，按 PID 复用排除
			103: {utime: 5, stime: 5},                                // 计数器回退
			106: {utime: 0, stime: 0},                                // 零增量
		},
	}
	samples := []procSample{
		{PID: 101, PPID: 1, Comm: "app", State: "S", Cmdline: "app --x", Utime: 200, Stime: 100, RSSBytes: 4096, IORead: 3000, IOWrite: 1500, Threads: 4},
		{PID: 102, PPID: 1, Comm: "hog", State: "S", Cmdline: "hog", Utime: 100000, Stime: 100000, RSSBytes: 8192, IORead: 5000, Threads: 2},
		{PID: 103, PPID: 1, Comm: "reset", State: "S", Cmdline: "reset", Utime: 1, Stime: 1, Threads: 1},
		{PID: 104, PPID: 1, Comm: "new", State: "S", Cmdline: "new", Utime: 50, RSSBytes: 100, Threads: 1},
		{PID: 105, PPID: 2, Comm: "kworker", State: "I", CmdlineEmpty: true, Threads: 1},
		{PID: 106, PPID: 1, Comm: "zombie", State: "Z", Cmdline: "zombie", Threads: 3},
		{PID: 107, PPID: 1, Comm: "tar", State: "D", Cmdline: "tar -x", Utime: 150, Stime: 50, RSSBytes: 2048, DelayBlkio: 42, Wchan: "io_schedule", Threads: 1},
	}

	got := buildSummary(samples, start, endTS, 4, 9)

	wantCounts := model.TopCounts{Processes: 7, Threads: 13, KernelThreads: 1, Zombies: 1, DState: 1, ListeningSockets: 9}
	if got.Counts != wantCounts {
		t.Fatalf("counts = %+v, want %+v", got.Counts, wantCounts)
	}
	if got.SchemaVersion != model.SchemaVersion {
		t.Fatalf("schema_version = %q", got.SchemaVersion)
	}
	if got.GeneratedAt != endTS.Format(time.RFC3339) {
		t.Fatalf("generated_at = %q", got.GeneratedAt)
	}

	if len(got.TopCPU) != 1 || got.TopCPU[0].PID != 101 || got.TopCPU[0].CPUMs != 1500 {
		t.Fatalf("top_cpu = %+v, want only pid 101 with 1500ms", got.TopCPU)
	}
	if len(got.TopRSS) != 4 || got.TopRSS[0].PID != 102 || got.TopRSS[1].PID != 101 || got.TopRSS[3].PID != 104 {
		t.Fatalf("top_rss = %+v", got.TopRSS)
	}
	// 101 取增量（3000-1000, 1500-500），102 窗口内已有但 IO 从 0 起算，
	// 104 没有开始记录（新进程）且 IO 为 0，故不出现。
	if len(got.TopIO) != 2 ||
		got.TopIO[0].PID != 102 || got.TopIO[0].ReadBytes != 5000 || got.TopIO[0].WriteBytes != 0 ||
		got.TopIO[1].PID != 101 || got.TopIO[1].ReadBytes != 2000 || got.TopIO[1].WriteBytes != 1000 {
		t.Fatalf("top_io = %+v, want [102:5000/0 101:2000/1000]", got.TopIO)
	}
	wantD := []model.DStateProc{{
		PID: 107, Comm: "tar", State: "D", Cmdline: "tar -x",
		Wchan: "io_schedule", DelayBlkio: 42,
	}}
	if !reflect.DeepEqual(got.DState, wantD) {
		t.Fatalf("dstate = %+v, want %+v", got.DState, wantD)
	}
}

func TestBuildSummaryCapsAndEmpty(t *testing.T) {
	samples := make([]procSample, 0, 25)
	for i := 1; i <= 25; i++ {
		samples = append(samples, procSample{
			PID: i, PPID: 1, Comm: "p" + strconv.Itoa(i), State: "S",
			RSSBytes: uint64(100000 - i),
		})
	}
	got := buildSummary(samples, nil, time.Now().UTC(), 0, 0)
	if len(got.TopRSS) != topN {
		t.Fatalf("top_rss len = %d, want %d", len(got.TopRSS), topN)
	}
	if got.TopRSS[0].PID != 1 || got.TopRSS[topN-1].PID != topN {
		t.Fatalf("top_rss order = first %d last %d", got.TopRSS[0].PID, got.TopRSS[topN-1].PID)
	}
	if got.TopCPU != nil || got.TopIO != nil || got.DState != nil {
		t.Fatalf("empty summary sections should be nil: %+v", got)
	}

	empty := buildSummary(nil, nil, time.Now().UTC(), 0, 0)
	if empty.Counts.Processes != 0 || empty.Counts.ListeningSockets != 0 {
		t.Fatalf("empty counts = %+v", empty.Counts)
	}
}

func TestTCPStateName(t *testing.T) {
	tests := []struct{ code, want string }{
		{"0A", "LISTEN"},
		{"0a", "LISTEN"},
		{"01", "ESTABLISHED"},
		{"06", "TIME_WAIT"},
		{"0F", "0F"},
	}
	for _, tt := range tests {
		if got := tcpStateName(tt.code); got != tt.want {
			t.Fatalf("tcpStateName(%q) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

// TestBeginEndSmoke 走一遍真实的 Begin/End 流程，校验落盘文件能按 model 解码且
// 满足 schema 的基本约束。macOS 上 /proc 不存在，只校验降级行为（空表 + 零计数）。
func TestBeginEndSmoke(t *testing.T) {
	dir := t.TempDir()
	env := &collect.Env{Stage: dir, RunID: "smoke", Host: model.Host{NCPU: 4}}
	if err := Begin(context.Background(), env); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := End(context.Background(), env); err != nil {
		t.Fatalf("End: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "top", "summary.json"))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	var sum model.TopSummary
	if err := json.Unmarshal(raw, &sum); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if sum.SchemaVersion != model.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", sum.SchemaVersion, model.SchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339, sum.GeneratedAt); err != nil {
		t.Fatalf("generated_at %q: %v", sum.GeneratedAt, err)
	}
	if n := sum.Counts.Processes; n < 0 {
		t.Fatalf("counts.processes = %d", n)
	}
	if len(sum.TopCPU) > topN || len(sum.TopRSS) > topN || len(sum.TopIO) > topN {
		t.Fatalf("top lists exceed %d: %d/%d/%d", topN, len(sum.TopCPU), len(sum.TopRSS), len(sum.TopIO))
	}
	if sum.Counts.KernelThreads > sum.Counts.Processes ||
		sum.Counts.Zombies > sum.Counts.Processes ||
		sum.Counts.DState > sum.Counts.Processes {
		t.Fatalf("counts inconsistent: %+v", sum.Counts)
	}
	if got := env.Counts().Processes; got != sum.Counts.Processes {
		t.Fatalf("env counts.processes = %d, summary = %d", got, sum.Counts.Processes)
	}
	if got := env.Counts().ListeningSockets; got != sum.Counts.ListeningSockets {
		t.Fatalf("env counts.listening_sockets = %d, summary = %d", got, sum.Counts.ListeningSockets)
	}

	raw, err = os.ReadFile(filepath.Join(dir, "top", "processes.jsonl"))
	if err != nil {
		t.Fatalf("read processes.jsonl: %v", err)
	}
	if len(raw) > 0 && !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("processes.jsonl does not end with newline")
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	seenPhase := make(map[string]bool)
	records := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec model.ProcessRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		records++
		seenPhase[rec.Phase] = true
		if rec.Phase != "start" && rec.Phase != "end" {
			t.Fatalf("line %d: phase = %q", i+1, rec.Phase)
		}
		if rec.PID <= 0 || rec.Comm == "" || rec.State == "" {
			t.Fatalf("line %d: missing required field: %+v", i+1, rec)
		}
		if utf8.RuneCountInString(rec.Comm) > 64 || len(rec.State) > 8 {
			t.Fatalf("line %d: comm/state too long: %q/%q", i+1, rec.Comm, rec.State)
		}
		if _, err := time.Parse(time.RFC3339, rec.TS); err != nil {
			t.Fatalf("line %d: ts %q: %v", i+1, rec.TS, err)
		}
		for _, arg := range rec.Cmdline {
			if utf8.RuneCountInString(arg) > 1024 {
				t.Fatalf("line %d: cmdline arg too long", i+1)
			}
		}
		if rec.PPID != nil && *rec.PPID < 0 {
			t.Fatalf("line %d: ppid = %d", i+1, *rec.PPID)
		}
		if rec.Threads != nil && *rec.Threads < 1 {
			t.Fatalf("line %d: threads = %d", i+1, *rec.Threads)
		}
		if rec.OOMScore != nil && (*rec.OOMScore < 0 || *rec.OOMScore > 1000) {
			t.Fatalf("line %d: oom_score = %d", i+1, *rec.OOMScore)
		}
		if rec.OOMAdj != nil && (*rec.OOMAdj < -1000 || *rec.OOMAdj > 1000) {
			t.Fatalf("line %d: oom_score_adj = %d", i+1, *rec.OOMAdj)
		}
	}

	if runtime.GOOS == "linux" {
		if records == 0 {
			t.Fatalf("expected process records on linux")
		}
		if !seenPhase["start"] || !seenPhase["end"] {
			t.Fatalf("phases = %v, want both start and end", seenPhase)
		}
	}
}

// fakeStat 生成结构合法的 /proc/<pid>/stat（40 个 comm 后字段）。
func fakeStat(pid int, comm, state string, ppid, utime, stime, threads int, delay uint64) string {
	fields := make([]string, 40)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = state
	fields[1] = strconv.Itoa(ppid)
	fields[2] = "1"
	fields[3] = "1"
	fields[11] = strconv.Itoa(utime)
	fields[12] = strconv.Itoa(stime)
	fields[17] = strconv.Itoa(threads)
	fields[19] = "130422"
	fields[39] = strconv.FormatUint(delay, 10)
	return fmt.Sprintf("%d (%s) %s", pid, comm, strings.Join(fields, " "))
}

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeTestLink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("symlink %s -> %s: %v", path, target, err)
	}
}

// TestBeginEndFakeProc 用临时目录伪造 procfs，端到端覆盖 Begin/End 的 Linux 路径：
// 字段读取、两次快照的 CPU 差、counts、top 列表、socket 反查与 D-state 列表。
func TestBeginEndFakeProc(t *testing.T) {
	root := t.TempDir()
	old := procPath
	procPath = root
	t.Cleanup(func() { procPath = old })

	mkStat := func(pid int, comm, state string, ppid, utime, stime, threads int, delay uint64) {
		writeTestFile(t, filepath.Join(root, strconv.Itoa(pid), "stat"),
			fakeStat(pid, comm, state, ppid, utime, stime, threads, delay)+"\n")
	}

	// pid 100：完整字段的普通进程。
	mkStat(100, "app", "S", 1, 100, 50, 4, 0)
	writeTestFile(t, filepath.Join(root, "100", "cmdline"), "app\x00--serve\x008080\x00")
	writeTestFile(t, filepath.Join(root, "100", "status"),
		"Name:\tapp\nUid:\t1001\t1001\t1001\t1001\nGid:\t1001\t1001\t1001\t1001\n"+
			"VmSize:\t 102400 kB\nVmRSS:\t 2048 kB\nThreads:\t4\n")
	writeTestFile(t, filepath.Join(root, "100", "io"), "rchar: 1\nread_bytes: 1000\nwrite_bytes: 500\n")
	writeTestFile(t, filepath.Join(root, "100", "oom_score"), "500\n")
	writeTestFile(t, filepath.Join(root, "100", "oom_score_adj"), "0\n")
	writeTestFile(t, filepath.Join(root, "100", "cgroup"), "0::/system.slice/app.service\n")
	writeTestLink(t, "/usr/bin/app", filepath.Join(root, "100", "exe"))
	writeTestLink(t, "/srv/app", filepath.Join(root, "100", "cwd"))
	writeTestLink(t, "/", filepath.Join(root, "100", "root"))
	writeTestLink(t, "pid:[4026531836]", filepath.Join(root, "100", "ns", "pid"))
	writeTestLink(t, "net:[4026531992]", filepath.Join(root, "100", "ns", "net"))
	writeTestLink(t, "socket:[38411]", filepath.Join(root, "100", "fd", "3"))
	writeTestLink(t, "socket:[111]", filepath.Join(root, "100", "fd", "4"))
	writeTestLink(t, "pipe:[99]", filepath.Join(root, "100", "fd", "5"))
	writeTestLink(t, "socket:[222]", filepath.Join(root, "100", "fd", "6"))

	// pid 200：内核线程（cmdline 为空 + ppid 2）。
	mkStat(200, "kworker/3:1", "I", 2, 0, 0, 1, 0)
	// pid 300：D-state 进程，有 delayacct 与 wchan。
	mkStat(300, "tar", "D", 1, 50, 10, 1, 42)
	writeTestFile(t, filepath.Join(root, "300", "cmdline"), "tar\x00-x\x00")
	writeTestFile(t, filepath.Join(root, "300", "status"),
		"Name:\ttar\nUid:\t0\t0\t0\t0\nGid:\t0\t0\t0\t0\nVmSize:\t 1024 kB\nVmRSS:\t 1024 kB\nThreads:\t1\n")
	writeTestFile(t, filepath.Join(root, "300", "wchan"), "io_schedule\n")
	// pid 400：僵尸。
	mkStat(400, "zombie", "Z", 1, 0, 0, 1, 0)
	// pid 500：只出现在开始快照。
	mkStat(500, "gone", "S", 1, 0, 0, 1, 0)

	writeTestFile(t, filepath.Join(root, "net", "tcp"),
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
			"   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 38411 1 0 0 0 0 0\n"+
			"   1: 0100007F:1F90 0100007F:C350 01 00000000:00000000 00:00000000 00000000     0        0 111 1 0 0 0 0 0\n")
	writeTestFile(t, filepath.Join(root, "net", "tcp6"),
		"  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
			"   0: 00000000000000000000000000000000:0050 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 222 1 0 0 0 0 0\n")

	env := &collect.Env{Stage: t.TempDir(), RunID: "fake-run", Host: model.Host{NCPU: 4}}
	if err := Begin(context.Background(), env); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// 把开始快照时间调早 10s，使 2 秒内的 CPU 增量不会撞上"墙钟×核数"上界。
	if v, ok := snapshots.Load("fake-run"); ok {
		v.(*snapshot).ts = time.Now().Add(-10 * time.Second)
	} else {
		t.Fatalf("snapshot not stored")
	}

	// 结束快照：更新计数器与 io，移除 500，新增 600。
	mkStat(100, "app", "S", 1, 300, 150, 4, 0)
	writeTestFile(t, filepath.Join(root, "100", "io"), "read_bytes: 3000\nwrite_bytes: 1500\n")
	mkStat(300, "tar", "D", 1, 60, 20, 1, 42)
	if err := os.RemoveAll(filepath.Join(root, "500")); err != nil {
		t.Fatalf("remove 500: %v", err)
	}
	mkStat(600, "newbie", "S", 1, 10, 0, 1, 0)
	writeTestFile(t, filepath.Join(root, "600", "cmdline"), "newbie\x00")
	writeTestFile(t, filepath.Join(root, "600", "io"), "read_bytes: 10\n")

	if err := End(context.Background(), env); err != nil {
		t.Fatalf("End: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(env.Stage, "top", "processes.jsonl"))
	if err != nil {
		t.Fatalf("read processes.jsonl: %v", err)
	}
	byPhasePID := make(map[string]map[int]model.ProcessRecord)
	for i, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec model.ProcessRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		if byPhasePID[rec.Phase] == nil {
			byPhasePID[rec.Phase] = make(map[int]model.ProcessRecord)
		}
		byPhasePID[rec.Phase][rec.PID] = rec
	}
	if len(byPhasePID["start"]) != 5 || len(byPhasePID["end"]) != 5 {
		t.Fatalf("start/end record counts = %d/%d, want 5/5", len(byPhasePID["start"]), len(byPhasePID["end"]))
	}
	if _, ok := byPhasePID["start"][600]; ok {
		t.Fatalf("pid 600 should not be in start snapshot")
	}
	if _, ok := byPhasePID["end"][500]; ok {
		t.Fatalf("pid 500 should not be in end snapshot")
	}

	start := byPhasePID["start"][100]
	if !reflect.DeepEqual(start.Cmdline, []string{"app", "--serve", "8080"}) {
		t.Fatalf("cmdline = %#v", start.Cmdline)
	}
	if start.Exe != "/usr/bin/app" || start.Cwd != "/srv/app" || start.Root != "/" {
		t.Fatalf("exe/cwd/root = %q/%q/%q", start.Exe, start.Cwd, start.Root)
	}
	if start.UID == nil || *start.UID != 1001 || start.GID == nil || *start.GID != 1001 {
		t.Fatalf("uid/gid = %v/%v", start.UID, start.GID)
	}
	if start.RSSBytes == nil || *start.RSSBytes != 2048*1024 {
		t.Fatalf("rss = %v", start.RSSBytes)
	}
	if start.VSZBytes == nil || *start.VSZBytes != 102400*1024 {
		t.Fatalf("vsz = %v", start.VSZBytes)
	}
	if start.Threads == nil || *start.Threads != 4 {
		t.Fatalf("threads = %v", start.Threads)
	}
	if start.IORead == nil || *start.IORead != 1000 || start.IOWrite == nil || *start.IOWrite != 500 {
		t.Fatalf("io = %v/%v", start.IORead, start.IOWrite)
	}
	if start.OOMScore == nil || *start.OOMScore != 500 || start.OOMAdj == nil || *start.OOMAdj != 0 {
		t.Fatalf("oom = %v/%v", start.OOMScore, start.OOMAdj)
	}
	if start.Cgroup != "/system.slice/app.service" || start.Unit != "app.service" {
		t.Fatalf("cgroup/unit = %q/%q", start.Cgroup, start.Unit)
	}
	if start.StartTicks == nil || *start.StartTicks != 130422 || start.UtimeTicks == nil || *start.UtimeTicks != 100 {
		t.Fatalf("ticks = %v/%v", start.StartTicks, start.UtimeTicks)
	}
	if start.NS == nil || start.NS.PID == nil || *start.NS.PID != 4026531836 ||
		start.NS.Net == nil || *start.NS.Net != 4026531992 || start.NS.Mnt != nil {
		t.Fatalf("ns = %+v", start.NS)
	}
	wantSockets := []model.SocketRef{
		{Proto: "tcp", Local: "0.0.0.0:8080", Remote: "0.0.0.0:0", State: "LISTEN", Inode: int64Ptr(38411)},
		{Proto: "tcp", Local: "127.0.0.1:8080", Remote: "127.0.0.1:50000", State: "ESTABLISHED", Inode: int64Ptr(111)},
		{Proto: "tcp6", Local: "[::]:80", Remote: "[::]:0", State: "LISTEN", Inode: int64Ptr(222)},
	}
	if !reflect.DeepEqual(start.Sockets, wantSockets) {
		t.Fatalf("sockets = %+v, want %+v", start.Sockets, wantSockets)
	}

	end := byPhasePID["end"][100]
	if end.UtimeTicks == nil || *end.UtimeTicks != 300 || end.IORead == nil || *end.IORead != 3000 {
		t.Fatalf("end ticks/io = %v/%v", end.UtimeTicks, end.IORead)
	}
	if kt := byPhasePID["end"][200]; kt.Cmdline != nil {
		t.Fatalf("kernel thread cmdline = %#v", kt.Cmdline)
	}

	raw, err = os.ReadFile(filepath.Join(env.Stage, "top", "summary.json"))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	var sum model.TopSummary
	if err := json.Unmarshal(raw, &sum); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	wantCounts := model.TopCounts{Processes: 5, Threads: 8, KernelThreads: 1, Zombies: 1, DState: 1, ListeningSockets: 2}
	if sum.Counts != wantCounts {
		t.Fatalf("counts = %+v, want %+v", sum.Counts, wantCounts)
	}
	wantCPU := []model.TopProcCPU{
		{PID: 100, Comm: "app", Cmdline: "app --serve 8080", CPUMs: 3000},
		{PID: 300, Comm: "tar", Cmdline: "tar -x", CPUMs: 200},
	}
	if !reflect.DeepEqual(sum.TopCPU, wantCPU) {
		t.Fatalf("top_cpu = %+v, want %+v", sum.TopCPU, wantCPU)
	}
	wantRSS := []model.TopProcRSS{
		{PID: 100, Comm: "app", Cmdline: "app --serve 8080", RSSBytes: 2048 * 1024},
		{PID: 300, Comm: "tar", Cmdline: "tar -x", RSSBytes: 1024 * 1024},
	}
	if !reflect.DeepEqual(sum.TopRSS, wantRSS) {
		t.Fatalf("top_rss = %+v, want %+v", sum.TopRSS, wantRSS)
	}
	wantIO := []model.TopProcIO{
		// 100 是增量（3000-1000, 1500-500）；600 在窗口内新建，累计值即增量。
		{PID: 100, Comm: "app", Cmdline: "app --serve 8080", ReadBytes: 2000, WriteBytes: 1000},
		{PID: 600, Comm: "newbie", Cmdline: "newbie", ReadBytes: 10},
	}
	if !reflect.DeepEqual(sum.TopIO, wantIO) {
		t.Fatalf("top_io = %+v, want %+v", sum.TopIO, wantIO)
	}
	wantDState := []model.DStateProc{
		{PID: 300, Comm: "tar", State: "D", Cmdline: "tar -x", Wchan: "io_schedule", DelayBlkio: 42},
	}
	if !reflect.DeepEqual(sum.DState, wantDState) {
		t.Fatalf("dstate = %+v, want %+v", sum.DState, wantDState)
	}
	if got := env.Counts().Processes; got != 5 {
		t.Fatalf("env counts.processes = %d", got)
	}
	if got := env.Counts().ListeningSockets; got != 2 {
		t.Fatalf("env counts.listening_sockets = %d", got)
	}
	if errs := env.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
}
