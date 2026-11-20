// Package collect 实现 M0 的基础信息采集：发行版、内核、CPU、内存、
// DMI/SMBIOS、sysctl、SELinux、cgroup、内核模块。
// 只读 /proc 与 /sys，不调用外部命令。
package collect

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"luanbird/osdiag/internal/model"
	"luanbird/osdiag/internal/pack"
	"luanbird/osdiag/internal/sysinfo"
)

type Result struct {
	Host         model.Host
	Capabilities []model.Capability
	Errors       []model.RunError
}

func (r *Result) addErr(collector string, err error) {
	r.Errors = append(r.Errors, model.RunError{Collector: collector, Msg: err.Error()})
}

// Collect 把基础信息写入 stage 目录。
func Collect(stage string) (*Result, error) {
	res := &Result{}
	host := sysinfo.HostInfo()
	res.Host = host
	res.Capabilities = sysinfo.Capabilities()

	if raw, err := os.ReadFile("/etc/os-release"); err == nil {
		if err := pack.WriteText(filepath.Join(stage, "meta/os-release.txt"), string(raw)); err != nil {
			return nil, err
		}
	} else {
		res.addErr("meta.os_release", err)
	}

	if err := pack.WriteJSON(filepath.Join(stage, "meta/host.json"), host); err != nil {
		return nil, err
	}

	kernel := map[string]any{
		"release": sysinfo.KernelRelease(),
		"version": sysinfo.KernelVersion(),
		"arch":    host.Arch,
		"cmdline": sysinfo.KernelCmdline(),
		"boot_id": host.BootID,
	}
	if v, ok := readTrim("/proc/sys/kernel/tainted"); ok {
		kernel["tainted"] = v
	}
	if err := pack.WriteJSON(filepath.Join(stage, "meta/kernel.json"), kernel); err != nil {
		return nil, err
	}

	cpu := map[string]any{
		"arch":       host.Arch,
		"ncpu":       host.NCPU,
		"model":      sysinfo.CPUModel(),
		"hypervisor": host.Env != "baremetal" && host.Env != "container",
	}
	if err := pack.WriteJSON(filepath.Join(stage, "meta/cpu.json"), cpu); err != nil {
		return nil, err
	}

	mem := sysinfo.Meminfo()
	memJSON := map[string]any{
		"mem_total_bytes":     mem["MemTotal"] * 1024,
		"mem_available_bytes": mem["MemAvailable"] * 1024,
		"mem_free_bytes":      mem["MemFree"] * 1024,
		"swap_total_bytes":    mem["SwapTotal"] * 1024,
	}
	if err := pack.WriteJSON(filepath.Join(stage, "meta/meminfo.json"), memJSON); err != nil {
		return nil, err
	}

	if err := pack.WriteJSON(filepath.Join(stage, "meta/sysctl.json"), sysinfo.Sysctl()); err != nil {
		return nil, err
	}
	if err := pack.WriteJSON(filepath.Join(stage, "meta/selinux.json"), sysinfo.SELinux()); err != nil {
		return nil, err
	}
	cgroup := map[string]any{
		"version":     sysinfo.CgroupVersion(),
		"controllers": fileExists("/sys/fs/cgroup/cgroup.controllers"),
	}
	if err := pack.WriteJSON(filepath.Join(stage, "meta/cgroup.json"), cgroup); err != nil {
		return nil, err
	}

	mods := sysinfo.Modules()
	var modText string
	for _, m := range mods {
		modText += m + "\n"
	}
	if err := pack.WriteText(filepath.Join(stage, "meta/modules.txt"), modText); err != nil {
		return nil, err
	}

	dmi := sysinfo.DMI()
	if len(dmi) > 0 {
		payload := map[string]any{"source": "sysfs", "fields": dmi}
		if err := pack.WriteJSON(filepath.Join(stage, "hardware/dmi.json"), payload); err != nil {
			return nil, err
		}
	} else {
		res.Errors = append(res.Errors, model.RunError{Collector: "hardware.dmi", Msg: "DMI 不可读（非 x86 或容器内无 /sys/class/dmi/id）"})
	}

	readme := `# osdiag 证据包

本包由 osdiag 在目标主机本地生成。工具只采集事实，不做研判。

- 先读 manifest.json 的 run.status、coverage.gaps；
- 再按 agent/reading-guide.md 的顺序阅读；
- 哈希见 integrity/SHA256SUMS，可用 osdiag inspect 校验。
`
	if err := pack.WriteText(filepath.Join(stage, "README.md"), readme); err != nil {
		return nil, err
	}

	overview := fmt.Sprintf(`# 诊断周期概览

- 主机：%s
- 发行版：%s
- 内核：%s（%s）
- 架构 / CPU / 内存：%s / %d vCPU / %.1f GiB
- 运行环境：%s
- 采集模式：M0 基础信息（只读，无指标采样、无主动探测）

本包只覆盖 meta 与 hardware/dmi；指标流、日志、探测与派生统计将在 M1+ 加入。
`, host.Hostname, host.OS.PrettyName, host.Kernel, host.Arch, host.Arch, host.NCPU,
		float64(host.MemBytes)/1024/1024/1024, host.Env)
	if err := pack.WriteText(filepath.Join(stage, "overview.md"), overview); err != nil {
		return nil, err
	}

	return res, nil
}

func readTrim(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
