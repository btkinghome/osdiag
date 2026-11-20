package metrics

import (
	"strings"
	"testing"
)

func TestParseProcStat(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantUser   uint64
		wantSystem uint64
		wantIdle   uint64
		wantCtxt   uint64
		wantRun    int
		wantBlock  int
		wantErr    bool
	}{
		{
			name: "typical",
			input: "cpu  100 20 30 800 40 5 6 7 8 9\n" +
				"cpu0 50 10 15 400 20 2 3 3 4 4\n" +
				"ctxt 123456\nbtime 1700000000\n" +
				"processes 999\nprocs_running 3\nprocs_blocked 2\n",
			wantUser: 100, wantSystem: 30, wantIdle: 800,
			wantCtxt: 123456, wantRun: 3, wantBlock: 2,
		},
		{
			name:     "short legacy fields",
			input:    "cpu 1 2 3 4 5 6 7 8\nctxt 9\nprocs_running 1\nprocs_blocked 0\n",
			wantUser: 1, wantSystem: 3, wantIdle: 4, wantCtxt: 9, wantRun: 1, wantBlock: 0,
		},
		{
			name:    "missing cpu line",
			input:   "ctxt 1\nprocs_running 0\n",
			wantErr: true,
		},
		{
			name:    "malformed cpu field",
			input:   "cpu 1 2 x 4 5 6 7 8\n",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseProcStat(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望错误，实际得到 %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got.cpu.user != tc.wantUser || got.cpu.system != tc.wantSystem || got.cpu.idle != tc.wantIdle {
				t.Fatalf("cpu 时间片不符: %+v", got.cpu)
			}
			if got.ctxt != tc.wantCtxt || got.procsRunning != tc.wantRun || got.procsBlocked != tc.wantBlock {
				t.Fatalf("ctxt/procs 不符: %+v", got)
			}
		})
	}
}

func TestCpuPercentages(t *testing.T) {
	prev := cpuTimes{user: 100, nice: 0, system: 50, idle: 850}
	cur := cpuTimes{user: 110, nice: 0, system: 60, idle: 930}
	got := cpuPercentages(cur, prev)
	if got.user != 10 || got.system != 10 || got.idle != 80 || got.iowait != 0 || got.steal != 0 {
		t.Fatalf("占比计算错误: %+v", got)
	}
	if got := cpuPercentages(prev, prev); got != (cpuPct{}) {
		t.Fatalf("无增量时应全为 0: %+v", got)
	}
	if d := counterDelta(1, 5); d != 0 {
		t.Fatalf("计数器回退应按 0 计，得到 %d", d)
	}
}

func TestParseLoadavg(t *testing.T) {
	got, err := parseLoadavg("0.42 0.35 0.30 2/345 6789\n")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got.one != 0.42 || got.five != 0.35 || got.fifteen != 0.30 {
		t.Fatalf("负载值不符: %+v", got)
	}
	if _, err := parseLoadavg("0.42 0.35"); err == nil {
		t.Fatal("字段不足时应报错")
	}
}

func TestParseMeminfo(t *testing.T) {
	input := `MemTotal:       16384000 kB
MemFree:         1024000 kB
MemAvailable:    8192000 kB
Buffers:          123456 kB
Cached:          2048000 kB
SwapTotal:             0 kB
Dirty:               128 kB
HugePages_Total:       4
HugePages_Free:        2
NoColonLine
BadValue:          abc kB
`
	got := parseMeminfo(input)
	cases := map[string]uint64{
		"MemTotal":        16384000 * 1024,
		"MemFree":         1024000 * 1024,
		"MemAvailable":    8192000 * 1024,
		"Buffers":         123456 * 1024,
		"Cached":          2048000 * 1024,
		"SwapTotal":       0,
		"Dirty":           128 * 1024,
		"HugePages_Total": 4,
		"HugePages_Free":  2,
	}
	for k, want := range cases {
		if got[k] != want {
			t.Errorf("%s = %d，期望 %d", k, got[k], want)
		}
	}
	if _, ok := got["BadValue"]; ok {
		t.Error("非法数值不应进入结果")
	}
}

func TestParseDiskStat(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantDev string
		want    diskStat
		wantErr bool
	}{
		{
			name:  "sysfs no prefix",
			input: "  12345 678 9012345 111 22222 333 4444555 666 7 888 999\n",
			want: diskStat{
				readIOs: 12345, readMerges: 678, readSectors: 9012345, readTicks: 111,
				writeIOs: 22222, writeMerges: 333, writeSectors: 4444555, writeTicks: 666,
				inFlight: 7, ioTicks: 888, timeInQueue: 999,
			},
		},
		{
			name:    "proc diskstats with prefix",
			input:   "8 0 sda 12345 678 9012345 111 22222 333 4444555 666 7 888 999 0 0 0\n",
			wantDev: "sda",
			want: diskStat{
				name: "sda", readIOs: 12345, readMerges: 678, readSectors: 9012345, readTicks: 111,
				writeIOs: 22222, writeMerges: 333, writeSectors: 4444555, writeTicks: 666,
				inFlight: 7, ioTicks: 888, timeInQueue: 999,
			},
		},
		{
			name:  "sysfs with extra future fields",
			input: "1 2 3 4 5 6 7 8 9 10 11 12 13 14 15",
			want: diskStat{
				readIOs: 1, readMerges: 2, readSectors: 3, readTicks: 4,
				writeIOs: 5, writeMerges: 6, writeSectors: 7, writeTicks: 8,
				inFlight: 9, ioTicks: 10, timeInQueue: 11,
			},
		},
		{
			name:    "too few fields",
			input:   "1 2 3",
			wantErr: true,
		},
		{
			name:    "non-numeric field",
			input:   "1 2 3 4 5 6 7 8 9 10 x",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDiskStat(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望错误，实际 %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got != tc.want {
				t.Fatalf("解析结果不符:\n got=%+v\nwant=%+v", got, tc.want)
			}
			if tc.wantDev != "" && got.name != tc.wantDev {
				t.Fatalf("设备名 = %q，期望 %q", got.name, tc.wantDev)
			}
		})
	}
}

func TestParseMountsAndFilter(t *testing.T) {
	input := `/dev/sda1 / ext4 rw,relatime 0 0
proc /proc proc rw,nosuid,nodev 0 0
/dev/sdb1 /data\040disk xfs ro,noatime 0 0
nfs-server:/export /mnt/nfs nfs4 rw 0 0
tmpfs /run tmpfs rw 0 0
`
	entries := parseMounts(input)
	if len(entries) != 5 {
		t.Fatalf("解析出 %d 条挂载，期望 5", len(entries))
	}
	if entries[2].mountpoint != "/data disk" {
		t.Fatalf("八进制转义未还原: %q", entries[2].mountpoint)
	}
	if !mountReadOnly(entries[2]) {
		t.Fatal("/data disk 应为只读挂载")
	}
	if mountReadOnly(entries[0]) {
		t.Fatal("/ 不应为只读挂载")
	}
	skipCases := []struct {
		idx      int
		wantSkip bool
	}{
		{0, false}, // ext4
		{1, true},  // proc
		{2, false}, // xfs
		{3, true},  // nfs4
		{4, true},  // tmpfs
	}
	for _, tc := range skipCases {
		got, _ := shouldSkipMount(entries[tc.idx])
		if got != tc.wantSkip {
			t.Errorf("挂载 %q 跳过 = %v，期望 %v", entries[tc.idx].mountpoint, got, tc.wantSkip)
		}
	}
}

func TestParsePressure(t *testing.T) {
	got := parsePressure("some avg10=0.00 avg60=0.10 avg300=0.20 total=12345\nfull avg10=0.01 avg60=0.02 avg300=0.03 total=9\n")
	if s := got["some"]; s.avg10 != 0 || s.avg60 != 0.10 || s.avg300 != 0.20 || s.total != 12345 {
		t.Fatalf("some 行解析错误: %+v", s)
	}
	if f := got["full"]; f.avg10 != 0.01 || f.total != 9 {
		t.Fatalf("full 行解析错误: %+v", f)
	}
}

func TestParseConntrackStat(t *testing.T) {
	header := "entries searched found new invalid ignore delete delete_list insert insert_failed drop early_drop icmp_error expect_new expect_create expect_delete search_restart"
	row1 := "0000000a 00000000 00000000 00000000 00000000 00000000 00000000 00000000 00000000 00000001 00000002 00000003 00000000 00000000 00000000 00000000 00000004"
	row2 := "00000005 00000000 00000000 00000000 00000000 00000000 00000000 00000000 00000000 00000002 00000000 00000000 00000000 00000000 00000000 00000000 00000001"
	got, err := parseConntrackStat(strings.Join([]string{header, row1, row2}, "\n"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	want := conntrackCounters{insertFailed: 3, drop: 2, earlyDrop: 3, searchRestart: 5}
	if got != want {
		t.Fatalf("统计求和错误: got=%+v want=%+v", got, want)
	}
}

func TestFieldDictionaryMatchesStreams(t *testing.T) {
	allowedKinds := map[string]bool{
		"counter": true, "gauge": true, "bytes": true, "enum": true, "string": true,
	}
	defs := streamDefinitions()
	defByID := make(map[string]*streamDef, len(defs))
	for _, d := range defs {
		defByID[d.id] = d
	}
	seen := make(map[string]map[string]bool, len(defs))
	counters := map[string]bool{
		"ctx_switches": true, "write_bytes_total": true,
	}
	for _, fd := range fieldDictionary {
		def, ok := defByID[fd.Stream]
		if !ok {
			t.Errorf("字段 %s 引用了未知流 %s", fd.Name, fd.Stream)
			continue
		}
		if seen[fd.Stream] == nil {
			seen[fd.Stream] = make(map[string]bool)
		}
		if seen[fd.Stream][fd.Name] {
			t.Errorf("字段 %s/%s 重复定义", fd.Stream, fd.Name)
		}
		seen[fd.Stream][fd.Name] = true
		if fd.Scope != def.scope {
			t.Errorf("字段 %s/%s scope=%q，期望 %q", fd.Stream, fd.Name, fd.Scope, def.scope)
		}
		if !allowedKinds[fd.Kind] {
			t.Errorf("字段 %s/%s kind=%q 不在允许集合", fd.Stream, fd.Name, fd.Kind)
		}
		if fd.Description == "" {
			t.Errorf("字段 %s/%s 缺少 description", fd.Stream, fd.Name)
		}
		if counters[fd.Name] && fd.Kind != "counter" {
			t.Errorf("字段 %s 应为 counter，实际 %s", fd.Name, fd.Kind)
		}
	}
	for _, d := range defs {
		if got := len(seen[d.id]); got != len(d.fields) {
			t.Errorf("流 %s 有 %d 个字段，字典覆盖 %d 个", d.id, len(d.fields), got)
			continue
		}
		for _, f := range d.fields {
			if !seen[d.id][f] {
				t.Errorf("流 %s 的字段 %s 缺少字典定义", d.id, f)
			}
		}
	}
}
