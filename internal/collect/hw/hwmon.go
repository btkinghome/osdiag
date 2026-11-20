package hw

import (
	"path/filepath"
	"strings"
)

// hwmonFile 对应 hardware/hwmon.json；thermal_zone 数据按设计并入本文件
// （任务允许 hwmon/thermal 合并为一个文件）。
type hwmonFile struct {
	Present      bool          `json:"present"`
	Chips        []hwmonChip   `json:"chips"`
	ThermalZones []thermalZone `json:"thermal_zones"`
	Notes        []string      `json:"notes,omitempty"`
}

type hwmonChip struct {
	Name  string      `json:"name,omitempty"`
	Path  string      `json:"path"`
	Temps []hwmonTemp `json:"temps,omitempty"`
	Fans  []hwmonFan  `json:"fans,omitempty"`
	Volts []hwmonVolt `json:"in_volts,omitempty"`
}

type hwmonTemp struct {
	Label         string   `json:"label,omitempty"`
	InputMillideg *int64   `json:"input_millideg,omitempty"`
	TempC         *float64 `json:"temp_c,omitempty"`
	MaxMillideg   *int64   `json:"max_millideg,omitempty"`
	CritMillideg  *int64   `json:"crit_millideg,omitempty"`
}

type hwmonFan struct {
	Label    string `json:"label,omitempty"`
	InputRPM *int64 `json:"input_rpm,omitempty"`
}

type hwmonVolt struct {
	Label   string `json:"label,omitempty"`
	InputMV *int64 `json:"input_mv,omitempty"`
}

type thermalZone struct {
	Name         string   `json:"name"`
	Type         string   `json:"type,omitempty"`
	TempMillideg *int64   `json:"temp_millideg,omitempty"`
	TempC        *float64 `json:"temp_c,omitempty"`
}

// milliToC 把 sysfs 的毫摄氏度换算成摄氏度。hwmon 的 temp*_input 与
// thermal_zone/temp 都以毫摄氏度为单位。
func milliToC(m int64) float64 {
	return float64(m) / 1000
}

// collectHwmon 读取 /sys/class/hwmon/hwmon*/ 与 /sys/class/thermal/thermal_zone*/。
func collectHwmon(root string) hwmonFile {
	out := hwmonFile{
		Chips:        []hwmonChip{},
		ThermalZones: []thermalZone{},
	}

	for _, dir := range sortedGlob(filepath.Join(root, "class/hwmon/hwmon*")) {
		if !dirExists(dir) {
			continue
		}
		chip := hwmonChip{
			Path: filepath.Base(dir),
			Name: readTrim(filepath.Join(dir, "name")),
		}
		chip.Temps = collectHwmonTemps(dir)
		chip.Fans = collectHwmonFans(dir)
		chip.Volts = collectHwmonVolts(dir)
		if chip.Name == "" && len(chip.Temps) == 0 && len(chip.Fans) == 0 && len(chip.Volts) == 0 {
			continue
		}
		out.Chips = append(out.Chips, chip)
	}

	for _, dir := range sortedGlob(filepath.Join(root, "class/thermal/thermal_zone*")) {
		if !dirExists(dir) {
			continue
		}
		z := thermalZone{
			Name: filepath.Base(dir),
			Type: readTrim(filepath.Join(dir, "type")),
		}
		if v, ok := readInt64(filepath.Join(dir, "temp")); ok {
			z.TempMillideg = ptrInt64(v)
			z.TempC = ptrFloat(milliToC(v))
		}
		if z.Type == "" && z.TempMillideg == nil {
			continue
		}
		out.ThermalZones = append(out.ThermalZones, z)
	}

	out.Present = len(out.Chips) > 0 || len(out.ThermalZones) > 0
	if !out.Present {
		out.Notes = append(out.Notes, "未发现 hwmon 芯片或 thermal zone（虚拟机/容器内常见）")
	}
	return out
}

func collectHwmonTemps(dir string) []hwmonTemp {
	paths := sortedGlob(filepath.Join(dir, "temp*_input"))
	if len(paths) > maxChannelFiles {
		paths = paths[:maxChannelFiles]
	}
	out := make([]hwmonTemp, 0, len(paths))
	for _, p := range paths {
		base := strings.TrimSuffix(p, "_input")
		milli, ok := readInt64(p)
		if !ok {
			continue
		}
		t := hwmonTemp{
			Label:         readTrim(base + "_label"),
			InputMillideg: ptrInt64(milli),
			TempC:         ptrFloat(milliToC(milli)),
			MaxMillideg:   readInt64Ptr(base + "_max"),
			CritMillideg:  readInt64Ptr(base + "_crit"),
		}
		out = append(out, t)
	}
	return out
}

func collectHwmonFans(dir string) []hwmonFan {
	paths := sortedGlob(filepath.Join(dir, "fan*_input"))
	if len(paths) > maxChannelFiles {
		paths = paths[:maxChannelFiles]
	}
	out := make([]hwmonFan, 0, len(paths))
	for _, p := range paths {
		rpm, ok := readInt64(p)
		if !ok {
			continue
		}
		out = append(out, hwmonFan{
			Label:    readTrim(strings.TrimSuffix(p, "_input") + "_label"),
			InputRPM: ptrInt64(rpm),
		})
	}
	return out
}

func collectHwmonVolts(dir string) []hwmonVolt {
	paths := sortedGlob(filepath.Join(dir, "in*_input"))
	if len(paths) > maxChannelFiles {
		paths = paths[:maxChannelFiles]
	}
	out := make([]hwmonVolt, 0, len(paths))
	for _, p := range paths {
		mv, ok := readInt64(p)
		if !ok {
			continue
		}
		out = append(out, hwmonVolt{
			Label:   readTrim(strings.TrimSuffix(p, "_input") + "_label"),
			InputMV: ptrInt64(mv),
		})
	}
	return out
}

func ptrFloat(v float64) *float64 { return &v }
