package hw

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"luanbird/osdiag/internal/collect"
)

// ---------- 测试辅助 ----------

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mkdirTest(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func int64Or(p *int64, def int64) int64 {
	if p == nil {
		return def
	}
	return *p
}

func intOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

// ---------- /proc/mdstat 解析 ----------

func TestParseMDStat(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []struct {
			name          string
			state         string
			level         string
			expectedDisks int // -1 表示 nil
			activeDisks   int // -1 表示 nil
			syncSub       string
		}
	}{
		{
			name: "空输入",
			raw:  "",
			want: nil,
		},
		{
			name: "只有 unused devices",
			raw:  "Personalities : [raid1]\nunused devices: <none>\n",
			want: nil,
		},
		{
			name: "raid1 完整",
			raw: `Personalities : [raid1] [raid6] [raid5] [raid4]
md0 : active raid1 sdb1[1] sda1[0]
      1046528 blocks super 1.2 [2/2] [UU]

unused devices: <none>
`,
			want: []struct {
				name          string
				state         string
				level         string
				expectedDisks int
				activeDisks   int
				syncSub       string
			}{
				{"md0", "active", "raid1", 2, 2, ""},
			},
		},
		{
			name: "raid5 降级并 resync",
			raw: `md1 : active raid5 sdc1[0] sdd1[1] sde1[2]
      2096128 blocks super 1.2 level 5, 512k chunk, algorithm 2 [3/2] [UU_]
      [=>...................]  recovery =  8.8% (184320/2096128) finish=1.2min speed=25000K/sec

`,
			want: []struct {
				name          string
				state         string
				level         string
				expectedDisks int
				activeDisks   int
				syncSub       string
			}{
				{"md1", "active", "raid5", 3, 2, "recovery =  8.8%"},
			},
		},
		{
			name: "无 blocks 行的 inactive 阵列",
			raw: `md2 : inactive sdf1[0]
      976630 blocks super 1.2

`,
			want: []struct {
				name          string
				state         string
				level         string
				expectedDisks int
				activeDisks   int
				syncSub       string
			}{
				{"md2", "inactive", "", -1, -1, ""},
			},
		},
		{
			name: "多阵列互不串扰",
			raw: `md0 : active raid1 sda1[0] sdb1[1]
      1046528 blocks super 1.2 [2/2] [UU]
md1 : active raid1 sdc1[0]
      1046528 blocks super 1.2 [2/1] [U_]

`,
			want: []struct {
				name          string
				state         string
				level         string
				expectedDisks int
				activeDisks   int
				syncSub       string
			}{
				{"md0", "active", "raid1", 2, 2, ""},
				{"md1", "active", "raid1", 2, 1, ""},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMDStat(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("阵列数量 = %d, want %d; got=%+v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				g := got[i]
				if g.Name != w.name || g.State != w.state || g.Level != w.level {
					t.Errorf("阵列[%d] = {%q %q %q}, want {%q %q %q}",
						i, g.Name, g.State, g.Level, w.name, w.state, w.level)
				}
				if got := intOr(g.DisksExpected, -1); got != w.expectedDisks {
					t.Errorf("阵列[%d] DisksExpected = %d, want %d", i, got, w.expectedDisks)
				}
				if got := intOr(g.DisksActive, -1); got != w.activeDisks {
					t.Errorf("阵列[%d] DisksActive = %d, want %d", i, got, w.activeDisks)
				}
				if w.syncSub != "" && !strings.Contains(g.SyncProgress, w.syncSub) {
					t.Errorf("阵列[%d] SyncProgress = %q, want 包含 %q", i, g.SyncProgress, w.syncSub)
				}
				if w.syncSub == "" && g.SyncProgress != "" {
					t.Errorf("阵列[%d] SyncProgress = %q, want 空", i, g.SyncProgress)
				}
			}
		})
	}
}

// ---------- AER 计数 ----------

func TestSumCounters(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int64
	}{
		{"空文本", "", 0},
		{"正常多行", "RxErr 3\nBadTLP 5\n", 8},
		{"跳过 TOTAL_ERR 汇总行", "RxErr 3\nTOTAL_ERR_COR 3\n", 3},
		{"十六进制回退", "RxErr 0x10\n", 16},
		{"无法解析的行忽略", "RxErr abc\nBadTLP 2\n", 2},
		{"字段不足忽略", "RxErr\n", 0},
		{"零值也参与", "RxErr 0\nBadTLP 0\n", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sumCounters(tc.in); got != tc.want {
				t.Fatalf("sumCounters(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestCollectAER(t *testing.T) {
	root := t.TempDir()
	dev := filepath.Join(root, "bus/pci/devices/0000:00:01.0")
	writeTestFile(t, filepath.Join(dev, "aer_dev_correctable"), "RxErr 3\nBadTLP 5\nTOTAL_ERR_COR 8\n")
	writeTestFile(t, filepath.Join(dev, "aer_dev_fatal"), "Uncorr internal 1\n")
	writeTestFile(t, filepath.Join(dev, "aer_dev_nonfatal"), "TLP header 2\n")
	// 没有 AER 计数文件的设备应被跳过。
	mkdirTest(t, filepath.Join(root, "bus/pci/devices/0000:00:02.0"))

	got := collectAER(root)
	if !got.Present {
		t.Fatal("Present = false, want true")
	}
	if len(got.Devices) != 1 {
		t.Fatalf("设备数 = %d, want 1: %+v", len(got.Devices), got.Devices)
	}
	d := got.Devices[0]
	if d.PCI != "0000:00:01.0" || d.Correctable != 8 || d.Fatal != 1 || d.Nonfatal != 2 {
		t.Fatalf("设备计数 = %+v, want correctable=8 fatal=1 nonfatal=2", d)
	}
	if got.Totals.Correctable != 8 || got.Totals.Fatal != 1 || got.Totals.Nonfatal != 2 {
		t.Fatalf("合计 = %+v, want {8 1 2}", got.Totals)
	}
}

func TestCollectAERAbsent(t *testing.T) {
	got := collectAER(t.TempDir())
	if got.Present {
		t.Fatal("Present = true, want false")
	}
	if len(got.Devices) != 0 {
		t.Fatalf("设备数 = %d, want 0", len(got.Devices))
	}
	if len(got.Notes) == 0 {
		t.Fatal("缺少 AER 时应有 notes 说明")
	}
}

// ---------- hwmon / thermal ----------

func TestMilliToC(t *testing.T) {
	tests := []struct {
		milli int64
		want  float64
	}{
		{45000, 45},
		{0, 0},
		{-5000, -5},
		{1234, 1.234},
		{999, 0.999},
	}
	for _, tc := range tests {
		got := milliToC(tc.milli)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("milliToC(%d) = %v, want %v", tc.milli, got, tc.want)
		}
	}
}

func TestCollectHwmon(t *testing.T) {
	root := t.TempDir()
	hw := filepath.Join(root, "class/hwmon/hwmon0")
	writeTestFile(t, filepath.Join(hw, "name"), "coretemp\n")
	writeTestFile(t, filepath.Join(hw, "temp1_input"), "45000\n")
	writeTestFile(t, filepath.Join(hw, "temp1_label"), "Core 0\n")
	writeTestFile(t, filepath.Join(hw, "temp1_max"), "100000\n")
	writeTestFile(t, filepath.Join(hw, "temp1_crit"), "110000\n")
	writeTestFile(t, filepath.Join(hw, "temp2_input"), "30000\n")
	writeTestFile(t, filepath.Join(hw, "fan1_input"), "1200\n")
	writeTestFile(t, filepath.Join(hw, "fan1_label"), "cpu_fan\n")
	writeTestFile(t, filepath.Join(hw, "in1_input"), "1200\n")
	writeTestFile(t, filepath.Join(hw, "in1_label"), "VCC\n")

	tz := filepath.Join(root, "class/thermal/thermal_zone0")
	writeTestFile(t, filepath.Join(tz, "type"), "x86_pkg_temp\n")
	writeTestFile(t, filepath.Join(tz, "temp"), "43000\n")
	// 空 thermal zone 应被跳过。
	mkdirTest(t, filepath.Join(root, "class/thermal/thermal_zone1"))

	got := collectHwmon(root)
	if !got.Present {
		t.Fatal("Present = false, want true")
	}
	if len(got.Chips) != 1 {
		t.Fatalf("芯片数 = %d, want 1", len(got.Chips))
	}
	chip := got.Chips[0]
	if chip.Name != "coretemp" {
		t.Errorf("chip.Name = %q, want coretemp", chip.Name)
	}
	if len(chip.Temps) != 2 {
		t.Fatalf("温度通道数 = %d, want 2", len(chip.Temps))
	}
	if chip.Temps[0].Label != "Core 0" {
		t.Errorf("temp1 label = %q, want Core 0", chip.Temps[0].Label)
	}
	if got := int64Or(chip.Temps[0].InputMillideg, -1); got != 45000 {
		t.Errorf("temp1 input = %d, want 45000", got)
	}
	if chip.Temps[0].TempC == nil || math.Abs(*chip.Temps[0].TempC-45) > 1e-9 {
		t.Errorf("temp1 temp_c = %v, want 45", chip.Temps[0].TempC)
	}
	if got := int64Or(chip.Temps[0].MaxMillideg, -1); got != 100000 {
		t.Errorf("temp1 max = %d, want 100000", got)
	}
	if got := int64Or(chip.Temps[0].CritMillideg, -1); got != 110000 {
		t.Errorf("temp1 crit = %d, want 110000", got)
	}
	if len(chip.Fans) != 1 || int64Or(chip.Fans[0].InputRPM, -1) != 1200 || chip.Fans[0].Label != "cpu_fan" {
		t.Errorf("风扇 = %+v, want cpu_fan 1200 RPM", chip.Fans)
	}
	if len(chip.Volts) != 1 || int64Or(chip.Volts[0].InputMV, -1) != 1200 || chip.Volts[0].Label != "VCC" {
		t.Errorf("电压 = %+v, want VCC 1200 mV", chip.Volts)
	}
	if len(got.ThermalZones) != 1 {
		t.Fatalf("thermal zone 数 = %d, want 1", len(got.ThermalZones))
	}
	zone := got.ThermalZones[0]
	if zone.Type != "x86_pkg_temp" {
		t.Errorf("zone.Type = %q, want x86_pkg_temp", zone.Type)
	}
	if zone.TempC == nil || math.Abs(*zone.TempC-43) > 1e-9 {
		t.Errorf("zone.temp_c = %v, want 43", zone.TempC)
	}
}

func TestCollectHwmonAbsent(t *testing.T) {
	got := collectHwmon(t.TempDir())
	if got.Present {
		t.Fatal("Present = true, want false")
	}
	if len(got.Notes) == 0 {
		t.Fatal("缺少 hwmon 时应有 notes 说明")
	}
}

// ---------- storage 汇总 ----------

func TestCollectStorage(t *testing.T) {
	sysRoot := t.TempDir()
	procRoot := t.TempDir()
	writeTestFile(t, filepath.Join(procRoot, "mdstat"), `Personalities : [raid1]
md0 : active raid1 sda1[0]
      1046528 blocks super 1.2 [2/2] [UU]

`)
	writeTestFile(t, filepath.Join(sysRoot, "block/md0/md/level"), "raid1\n")
	writeTestFile(t, filepath.Join(sysRoot, "block/md0/md/raid_disks"), "2\n")
	writeTestFile(t, filepath.Join(sysRoot, "block/md0/md/degraded"), "0\n")
	writeTestFile(t, filepath.Join(sysRoot, "block/md0/md/sync_action"), "idle\n")

	writeTestFile(t, filepath.Join(sysRoot, "block/dm-0/dm/name"), "vg-root\n")
	writeTestFile(t, filepath.Join(sysRoot, "block/dm-0/dm/uuid"), "abcd-1234\n")
	mkdirTest(t, filepath.Join(sysRoot, "block/dm-0/slaves"))
	writeTestFile(t, filepath.Join(sysRoot, "block/dm-0/slaves/md0"), "")

	sda := filepath.Join(sysRoot, "block/sda")
	writeTestFile(t, filepath.Join(sda, "device/model"), "Samsung SSD 870\n")
	writeTestFile(t, filepath.Join(sda, "device/vendor"), "ATA\n")
	writeTestFile(t, filepath.Join(sda, "device/serial"), "S123\n")
	writeTestFile(t, filepath.Join(sda, "device/firmware_rev"), "1.0\n")
	writeTestFile(t, filepath.Join(sda, "queue/rotational"), "1\n")
	writeTestFile(t, filepath.Join(sda, "size"), "1953525168\n")

	got := collectStorage(sysRoot, procRoot)
	if !got.Present || !got.MDStat.Present {
		t.Fatalf("Present = %v / MDStat.Present = %v, want true", got.Present, got.MDStat.Present)
	}
	if !strings.Contains(got.MDStat.Raw, "md0 : active raid1") {
		t.Errorf("mdstat raw = %q", got.MDStat.Raw)
	}
	if len(got.MDArrays) != 1 {
		t.Fatalf("md 阵列数 = %d, want 1: %+v", len(got.MDArrays), got.MDArrays)
	}
	arr := got.MDArrays[0]
	if arr.Name != "md0" || arr.Level != "raid1" || arr.State != "active" || arr.SyncAction != "idle" {
		t.Errorf("md 阵列 = %+v", arr)
	}
	if int64Or(arr.RaidDisks, -1) != 2 || int64Or(arr.Degraded, -1) != 0 {
		t.Errorf("raid_disks/degraded = %v/%v, want 2/0", arr.RaidDisks, arr.Degraded)
	}
	if intOr(arr.DisksExpected, -1) != 2 || intOr(arr.DisksActive, -1) != 2 {
		t.Errorf("mdstat [n/m] = %v/%v, want 2/2", arr.DisksExpected, arr.DisksActive)
	}

	if len(got.DMDevices) != 1 {
		t.Fatalf("DM 设备数 = %d, want 1", len(got.DMDevices))
	}
	dm := got.DMDevices[0]
	if dm.DMName != "vg-root" || dm.UUID != "abcd-1234" {
		t.Errorf("DM = %+v", dm)
	}
	if len(dm.Slaves) != 1 || dm.Slaves[0] != "md0" {
		t.Errorf("DM slaves = %v, want [md0]", dm.Slaves)
	}

	var found *blockDevice
	for i := range got.BlockDevices {
		if got.BlockDevices[i].Name == "sda" {
			found = &got.BlockDevices[i]
		}
	}
	if found == nil {
		t.Fatalf("块设备列表缺少 sda: %+v", got.BlockDevices)
	}
	if found.Model != "Samsung SSD 870" || found.Vendor != "ATA" || found.Serial != "S123" || found.Firmware != "1.0" {
		t.Errorf("sda 身份 = %+v", found)
	}
	if found.Rotational == nil || !*found.Rotational {
		t.Errorf("sda rotational = %v, want true", found.Rotational)
	}
	if got := int64Or(found.SizeBytes, -1); got != 1953525168*512 {
		t.Errorf("sda size_bytes = %d, want %d", got, int64(1953525168*512))
	}
}

func TestCollectStorageAbsent(t *testing.T) {
	got := collectStorage(t.TempDir(), t.TempDir())
	if got.Present || got.MDStat.Present {
		t.Fatalf("Present = %v / MDStat.Present = %v, want false", got.Present, got.MDStat.Present)
	}
	if got.MDArrays == nil || got.DMDevices == nil || got.BlockDevices == nil {
		t.Fatal("空环境下数组应为空切片而不是 nil")
	}
}

// ---------- EDAC ----------

func TestCollectEDAC(t *testing.T) {
	root := t.TempDir()
	mc := filepath.Join(root, "devices/system/edac/mc/mc0")
	writeTestFile(t, filepath.Join(mc, "ce_count"), "3\n")
	writeTestFile(t, filepath.Join(mc, "ue_count"), "1\n")
	writeTestFile(t, filepath.Join(mc, "ce_noinfo_count"), "2\n")
	writeTestFile(t, filepath.Join(mc, "ue_noinfo_count"), "0\n")
	writeTestFile(t, filepath.Join(mc, "dimm0/dimm_label"), "DIMM_A1\n")
	writeTestFile(t, filepath.Join(mc, "dimm0/size"), "16384\n")

	got := collectEDAC(root)
	if !got.Present || len(got.Controllers) != 1 {
		t.Fatalf("Present = %v, controllers = %d, want true/1", got.Present, len(got.Controllers))
	}
	c := got.Controllers[0]
	if c.Name != "mc0" || c.CECount != 3 || c.UECount != 1 || c.CENoinfo != 2 || c.UENoinfo != 0 {
		t.Errorf("EDAC 计数 = %+v", c)
	}
	if len(c.DIMMs) != 1 || c.DIMMs[0].Label != "DIMM_A1" || int64Or(c.DIMMs[0].SizeMB, -1) != 16384 {
		t.Errorf("DIMM = %+v", c.DIMMs)
	}
	if len(c.DIMMLabels) != 1 || c.DIMMLabels[0] != "DIMM_A1" {
		t.Errorf("DIMMLabels = %v", c.DIMMLabels)
	}
}

func TestCollectEDACAbsent(t *testing.T) {
	got := collectEDAC(t.TempDir())
	if got.Present {
		t.Fatal("Present = true, want false")
	}
	if got.Sources == nil || got.Controllers == nil {
		t.Fatal("空环境下 sources/controllers 应为空切片")
	}
}

// ---------- SMART 枚举 ----------

func TestSmartCandidates(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "block/nvme0n1/device/model"), "NVMe Model\n")
	writeTestFile(t, filepath.Join(root, "block/nvme0n1/device/serial"), "NVMESER\n")
	writeTestFile(t, filepath.Join(root, "block/sda/device/model"), "SATA Model\n")
	writeTestFile(t, filepath.Join(root, "block/sda/device/vendor"), "ATA\n")
	mkdirTest(t, filepath.Join(root, "block/vda"))
	mkdirTest(t, filepath.Join(root, "block/mmcblk0"))
	mkdirTest(t, filepath.Join(root, "block/loop0"))
	mkdirTest(t, filepath.Join(root, "block/dm-0"))
	mkdirTest(t, filepath.Join(root, "block/md0"))

	cands, unsupported, _, truncated := smartCandidates(root)
	if truncated {
		t.Error("truncated = true, want false")
	}
	if len(cands) != 2 {
		t.Fatalf("候选盘数 = %d, want 2: %+v", len(cands), cands)
	}
	if cands[0].Name != "nvme0n1" || cands[0].Kind != "nvme" {
		t.Errorf("候选[0] = %+v, want nvme0n1/nvme", cands[0])
	}
	if cands[0].Model != "NVMe Model" || cands[0].Serial != "NVMESER" {
		t.Errorf("NVMe 身份 = %+v", cands[0])
	}
	if cands[1].Name != "sda" || cands[1].Kind != "ata" {
		t.Errorf("候选[1] = %+v, want sda/ata", cands[1])
	}
	if len(unsupported) != 2 {
		t.Fatalf("unsupported 数 = %d, want 2: %+v", len(unsupported), unsupported)
	}
	if unsupported[0].Device != "/dev/mmcblk0" || unsupported[1].Device != "/dev/vda" {
		t.Errorf("unsupported = %+v", unsupported)
	}
}

func TestSmartCandidatesCap(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < smartMaxDisks+5; i++ {
		mkdirTest(t, filepath.Join(root, "block", fmt.Sprintf("sd%02d", i)))
	}
	cands, _, notes, truncated := smartCandidates(root)
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(cands) > smartMaxDisks {
		t.Fatalf("候选盘数 = %d, 超过上限 %d", len(cands), smartMaxDisks)
	}
	if len(notes) == 0 {
		t.Fatal("截断时应有 notes 说明")
	}
}

func TestCollectSMARTAbsent(t *testing.T) {
	got := collectSMART(context.Background(), t.TempDir())
	if got.Present {
		t.Fatal("Present = true, want false")
	}
	if got.Devices == nil || got.Errors == nil || got.Unsupported == nil {
		t.Fatal("空环境下 devices/errors/unsupported 应为空切片")
	}
}

// ---------- Run 端到端 ----------

func TestRunWritesAllFiles(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("避免在 Linux 真机上执行 SMART ioctl")
	}
	stage := t.TempDir()
	env := collect.NewEnv(stage, "run-test", "", "quick", time.Now(), time.Minute)
	if err := Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, rel := range []string{
		"hardware/edac.json",
		"hardware/hwmon.json",
		"hardware/storage.json",
		"hardware/aer.json",
		"hardware/pci.json",
		"hardware/pstore.json",
		"hardware/smart.json",
	} {
		path := env.Path(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s: %v", rel, err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("%s 不是合法 JSON: %v", rel, err)
		}
		if _, ok := m["present"]; !ok {
			t.Errorf("%s 缺少 present 字段", rel)
		}
	}
	if errs := env.Errors(); len(errs) != 0 {
		t.Fatalf("Run 记录了错误: %+v", errs)
	}
}

func TestRunNilEnv(t *testing.T) {
	if err := Run(context.Background(), nil); err == nil {
		t.Fatal("Run(nil) 应返回错误")
	}
}

func TestRunCanceledContext(t *testing.T) {
	stage := t.TempDir()
	env := collect.NewEnv(stage, "run-cancel", "", "quick", time.Now(), time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, env); err != context.Canceled {
		t.Fatalf("Run(canceled) = %v, want context.Canceled", err)
	}
}
