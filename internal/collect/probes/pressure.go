package probes

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

const (
	// memAvailableMin 是重项探测的内存下限（设计稿 6.10）。
	memAvailableMin = 512 << 20
	// psiIOMax 是 PSI io avg10 的百分比上限。
	psiIOMax = 30.0
)

// pressureSample 汇总探测前保护需要的压力指标；*OK 为 false 表示该检查不可用。
type pressureSample struct {
	Load1             float64
	LoadOK            bool
	MemAvailableBytes uint64
	MemOK             bool
	PSIAvg10          float64
	PSIOK             bool
}

// evaluatePressure 按设计稿阈值判定是否跳过 P1/P2。
// 不存在的检查项直接忽略（例如容器内没有 /proc/pressure/io）。
func evaluatePressure(p pressureSample, ncpu int) (bool, string) {
	if ncpu <= 0 {
		ncpu = runtime.NumCPU()
	}
	var reasons []string
	if p.LoadOK && p.Load1 > 2*float64(ncpu) {
		reasons = append(reasons, fmt.Sprintf("load1=%.2f>2*ncpu=%.1f", p.Load1, 2*float64(ncpu)))
	}
	if p.PSIOK && p.PSIAvg10 > psiIOMax {
		reasons = append(reasons, fmt.Sprintf("psi_io_avg10=%.1f>%.0f", p.PSIAvg10, psiIOMax))
	}
	if p.MemOK && p.MemAvailableBytes < memAvailableMin {
		reasons = append(reasons, fmt.Sprintf("MemAvailable=%dMiB<512MiB", p.MemAvailableBytes>>20))
	}
	if len(reasons) == 0 {
		return false, ""
	}
	return true, strings.Join(reasons, "; ")
}

// readPressure 读取 /proc/loadavg、MemAvailable 与 PSI io；失败项标记为不可用。
func readPressure() pressureSample {
	var p pressureSample
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if v, ok := parseLoad1(string(b)); ok {
			p.Load1, p.LoadOK = v, true
		}
	}
	if v, ok := readMemAvailable(); ok {
		p.MemAvailableBytes, p.MemOK = v, true
	}
	// cgroup v2 的 io.pressure 在 /proc/pressure/io 不可见时作为回退。
	for _, path := range []string{"/proc/pressure/io", "/sys/fs/cgroup/io.pressure"} {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if v, ok := parsePSIIOAvg10(string(b)); ok {
			p.PSIAvg10, p.PSIOK = v, true
			break
		}
	}
	return p
}

func parseLoad1(s string) (float64, bool) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// readMemAvailable 返回 /proc/meminfo 的 MemAvailable 字节数。
func readMemAvailable() (uint64, bool) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	return parseMemAvailable(string(b))
}

// parseMemAvailable 解析 meminfo 文本中的 MemAvailable（kB → bytes）。
func parseMemAvailable(s string) (uint64, bool) {
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}

// parsePSIIOAvg10 解析 PSI 文本，取 some/full 中较大的 avg10。
func parsePSIIOAvg10(s string) (float64, bool) {
	var best float64
	found := false
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "some ") && !strings.HasPrefix(line, "full ") {
			continue
		}
		for _, f := range strings.Fields(line) {
			if !strings.HasPrefix(f, "avg10=") {
				continue
			}
			v, err := strconv.ParseFloat(strings.TrimPrefix(f, "avg10="), 64)
			if err != nil {
				return 0, false
			}
			if !found || v > best {
				best, found = v, true
			}
		}
	}
	return best, found
}
