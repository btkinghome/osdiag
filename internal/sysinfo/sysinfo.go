// Package sysinfo 从 /proc、/sys 读取主机基础信息。
// 只用 Go 标准库；不调用任何外部命令。
package sysinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"luanbird/osdiag/internal/model"
)

func readTrim(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// ParseOSRelease 解析 /etc/os-release 的 KEY=VALUE 行，去掉引号。
func ParseOSRelease(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 {
			if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
				v = v[1 : len(v)-1]
			}
		}
		out[k] = v
	}
	return out
}

func OSInfo() model.OS {
	raw, ok := readTrim("/etc/os-release")
	kv := map[string]string{}
	if ok {
		kv = ParseOSRelease(raw)
	}
	release := KernelRelease()
	id, pretty := kv["ID"], kv["PRETTY_NAME"]
	if id == "" {
		id = "unknown"
	}
	if pretty == "" {
		pretty = "unknown"
	}
	return model.OS{
		ID:               id,
		VersionID:        kv["VERSION_ID"],
		PrettyName:       pretty,
		KernelBaselineOK: KernelBaselineOK(release),
	}
}

// KernelRelease 读 /proc/sys/kernel/osrelease；非 Linux 返回 "unknown"。
func KernelRelease() string {
	if v, ok := readTrim("/proc/sys/kernel/osrelease"); ok && v != "" {
		return v
	}
	return "unknown"
}

func KernelVersion() string {
	if v, ok := readTrim("/proc/sys/kernel/version"); ok {
		return v
	}
	return ""
}

func KernelCmdline() string {
	if v, ok := readTrim("/proc/cmdline"); ok {
		return v
	}
	return ""
}

var kernelRe = regexp.MustCompile(`^(\d+)\.(\d+)`)

// KernelBaselineOK 判断内核是否 >= 4.19（设计稿 §11.1 的最低基线）。
func KernelBaselineOK(release string) bool {
	m := kernelRe.FindStringSubmatch(release)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major != 4 {
		return major > 4
	}
	return minor >= 19
}

func MachineArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "loong64":
		return "loongarch64"
	default:
		return runtime.GOARCH
	}
}

// Meminfo 返回 /proc/meminfo 的原始键值（单位 kB，未换算）。
func Meminfo() map[string]uint64 {
	out := map[string]uint64{}
	raw, ok := readTrim("/proc/meminfo")
	if !ok {
		return out
	}
	for _, line := range strings.Split(raw, "\n") {
		k, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		out[strings.TrimSpace(k)] = n
	}
	return out
}

func MemTotalBytes() uint64 {
	if v, ok := Meminfo()["MemTotal"]; ok {
		return v * 1024
	}
	return 0
}

func cpuInfoRaw() string {
	raw, _ := readTrim("/proc/cpuinfo")
	return raw
}

// NCPU 统计 /proc/cpuinfo 的 processor 行，回退到 runtime.NumCPU。
func NCPU() int {
	n := 0
	for _, line := range strings.Split(cpuInfoRaw(), "\n") {
		if strings.HasPrefix(line, "processor") {
			n++
		}
	}
	if n == 0 {
		n = runtime.NumCPU()
	}
	return n
}

func CPUModel() string {
	for _, line := range strings.Split(cpuInfoRaw(), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == "model name" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func hasHypervisorFlag() bool {
	for _, line := range strings.Split(cpuInfoRaw(), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == "flags" {
			for _, f := range strings.Fields(v) {
				if f == "hypervisor" {
					return true
				}
			}
		}
	}
	return false
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// DetectEnv 判断运行环境，返回 manifest.host.env 的枚举值与 container 标记。
func DetectEnv() (string, bool) {
	if containerized() {
		return "container", true
	}
	vendor, _ := readTrim("/sys/class/dmi/id/sys_vendor")
	product, _ := readTrim("/sys/class/dmi/id/product_name")
	s := strings.ToLower(vendor + " " + product)
	switch {
	case strings.Contains(s, "vmware"):
		return "vmware", false
	case strings.Contains(s, "xen"):
		return "xen", false
	case strings.Contains(s, "qemu"), strings.Contains(s, "kvm"), strings.Contains(s, "bochs"):
		return "kvm", false
	case strings.Contains(s, "microsoft") && strings.Contains(s, "virtual"):
		return "hyperv", false
	case hasHypervisorFlag():
		return "unknown", false
	default:
		return "baremetal", false
	}
}

func containerized() bool {
	if fileExists("/.dockerenv") || fileExists("/run/.containerenv") {
		return true
	}
	if raw, ok := readTrim("/proc/1/cgroup"); ok {
		for _, marker := range []string{"docker", "kubepods", "containerd", "libpod", "lxc"} {
			if strings.Contains(raw, marker) {
				return true
			}
		}
	}
	return false
}

// DMI 读取 /sys/class/dmi/id 下的只读属性。
func DMI() map[string]string {
	out := map[string]string{}
	keys := []string{
		"sys_vendor", "product_name", "product_version", "product_serial", "product_uuid",
		"board_vendor", "board_name", "board_serial", "board_version",
		"bios_vendor", "bios_version", "bios_date",
		"chassis_vendor", "chassis_serial", "chassis_type",
	}
	for _, k := range keys {
		if v, ok := readTrim(filepath.Join("/sys/class/dmi/id", k)); ok {
			if v = cleanDMI(v); v != "" {
				out[k] = v
			}
		}
	}
	return out
}

// cleanDMI 去掉固件里的占位串，避免把 "None" 当成序列号。
func cleanDMI(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "none", "not specified", "not applicable", "n/a", "unknown",
		"to be filled by o.e.m.", "to be filled by oem", "default string",
		"system serial number", "not available", "chassis serial number":
		return ""
	}
	return strings.TrimSpace(v)
}

func MachineID() string {
	if v, ok := readTrim("/etc/machine-id"); ok && v != "" {
		return v
	}
	if v, ok := readTrim("/var/lib/dbus/machine-id"); ok {
		return v
	}
	return ""
}

func BootID() string {
	v, _ := readTrim("/proc/sys/kernel/random/boot_id")
	return v
}

func HashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// HostInfo 组装 manifest.host。
func HostInfo() model.Host {
	hostname, _ := os.Hostname()
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		hostname = "unknown"
	}
	env, container := DetectEnv()
	dmi := DMI()
	machineID := MachineID()

	fpSeed := machineID + "\x00" + dmi["product_uuid"]
	if machineID == "" && dmi["product_uuid"] == "" {
		fpSeed = hostname + "\x00" + KernelRelease()
	}

	h := model.Host{
		Hostname:    hostname,
		Fingerprint: HashString(fpSeed),
		Kernel:      KernelRelease(),
		Arch:        MachineArch(),
		NCPU:        NCPU(),
		MemBytes:    MemTotalBytes(),
		Env:         env,
		Container:   container,
		OS:          OSInfo(),
	}
	if strings.Contains(hostname, ".") {
		h.FQDN = hostname
	}
	if machineID == "" {
		machineID = hostname + "\x00" + KernelRelease()
	}
	h.MachineIDHash = HashString(machineID)
	h.BootID = BootID()
	if h.BootID == "" {
		h.BootID = "unknown"
	}

	serials := model.Serials{
		System:  dmi["product_serial"],
		Board:   dmi["board_serial"],
		Product: dmi["product_name"],
		Source:  "sysfs",
	}
	if serials.System != "" || serials.Board != "" || serials.Product != "" {
		h.Serials = &serials
	}
	return h
}

// Sysctl 读取一组与诊断相关的 /proc/sys 键；缺失的键不出现。
func Sysctl() map[string]string {
	keys := []string{
		"kernel.panic", "kernel.panic_on_oops", "kernel.tainted", "kernel.randomize_va_space",
		"kernel.pid_max", "kernel.threads-max", "kernel.numa_balancing", "kernel.core_pattern",
		"kernel.unprivileged_bpf_disabled", "kernel.kptr_restrict", "kernel.dmesg_restrict",
		"vm.swappiness", "vm.overcommit_memory", "vm.min_free_kbytes", "vm.dirty_ratio",
		"vm.dirty_background_ratio", "vm.panic_on_oom", "vm.max_map_count", "vm.oom_kill_allocating_task",
		"net.core.somaxconn", "net.core.netdev_max_backlog", "net.core.rmem_max", "net.core.wmem_max",
		"net.core.netdev_budget", "net.ipv4.tcp_syncookies", "net.ipv4.tcp_syn_retries",
		"net.ipv4.tcp_tw_reuse", "net.ipv4.tcp_retries2", "net.ipv4.ip_local_port_range",
		"net.ipv4.tcp_max_syn_backlog", "net.ipv6.conf.all.disable_ipv6",
		"fs.file-max", "fs.nr_open", "fs.inotify.max_user_watches", "fs.inotify.max_user_instances",
	}
	out := map[string]string{}
	for _, k := range keys {
		p := filepath.Join("/proc/sys", strings.ReplaceAll(k, ".", "/"))
		if v, ok := readTrim(p); ok {
			out[k] = strings.Join(strings.Fields(v), " ")
		}
	}
	return out
}

// Modules 返回已加载内核模块名列表。
func Modules() []string {
	raw, ok := readTrim("/proc/modules")
	if !ok {
		return nil
	}
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) > 0 {
			out = append(out, f[0])
		}
	}
	return out
}

func SELinux() map[string]any {
	if !fileExists("/sys/fs/selinux") {
		return map[string]any{"enabled": false, "mode": "disabled"}
	}
	mode := "enabled"
	if v, ok := readTrim("/sys/fs/selinux/enforce"); ok {
		if v == "1" {
			mode = "enforcing"
		} else {
			mode = "permissive"
		}
	}
	return map[string]any{"enabled": true, "mode": mode}
}

func CgroupVersion() string {
	if fileExists("/sys/fs/cgroup/cgroup.controllers") {
		return "v2"
	}
	if fileExists("/sys/fs/cgroup") {
		return "v1"
	}
	return "unknown"
}

// Capabilities 探测可选能力；readable 只表示当前权限下能否读到。
func Capabilities() []model.Capability {
	type probe struct {
		id   string
		path string
	}
	probes := []probe{
		{"smbios", "/sys/class/dmi/id"},
		{"dmi_tables", "/sys/firmware/dmi/tables"},
		{"edac", "/sys/devices/system/edac/mc"},
		{"pstore", "/sys/fs/pstore"},
		{"journald", "/var/log/journal"},
		{"journald_runtime", "/run/log/journal"},
		{"psi", "/proc/pressure"},
		{"selinux", "/sys/fs/selinux"},
		{"pcie_aer", "/sys/kernel/debug/pcie/aer"},
		{"hwmon", "/sys/class/hwmon"},
		{"thermal", "/sys/class/thermal"},
		{"cgroup_v2", "/sys/fs/cgroup/cgroup.controllers"},
	}
	out := make([]model.Capability, 0, len(probes))
	for _, p := range probes {
		if p.id == "journald" || p.id == "journald_runtime" {
			continue // 合并为下方一个条目
		}
		st, err := os.Stat(p.path)
		cap := model.Capability{ID: p.id}
		if err == nil {
			cap.Present = true
			if st.IsDir() {
				_, rerr := os.ReadDir(p.path)
				cap.Readable = rerr == nil
				if rerr != nil {
					cap.Errno = "EACCES"
				}
			} else {
				_, rerr := os.ReadFile(p.path)
				cap.Readable = rerr == nil
				if rerr != nil {
					cap.Errno = "EACCES"
				}
			}
		}
		out = append(out, cap)
	}

	journal := model.Capability{ID: "journald"}
	for _, p := range []string{"/var/log/journal", "/run/log/journal"} {
		if fileExists(p) {
			journal.Present = true
			if _, err := os.ReadDir(p); err == nil {
				journal.Readable = true
			} else {
				journal.Errno = "EACCES"
			}
		}
	}
	out = append(out, journal)
	return out
}

// SelfOverhead 从 /proc/self 读取本进程资源消耗；非 Linux 返回零值。
func SelfOverhead() model.Overhead {
	var o model.Overhead
	if raw, ok := readTrim("/proc/self/status"); ok {
		for _, line := range strings.Split(raw, "\n") {
			if v, found := strings.CutPrefix(line, "VmHWM:"); found {
				f := strings.Fields(v)
				if len(f) > 0 {
					o.RSSKB, _ = strconv.ParseInt(f[0], 10, 64)
				}
			}
		}
	}
	if raw, ok := readTrim("/proc/self/io"); ok {
		for _, line := range strings.Split(raw, "\n") {
			if v, found := strings.CutPrefix(line, "read_bytes:"); found {
				o.ReadBytes, _ = strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			}
			if v, found := strings.CutPrefix(line, "write_bytes:"); found {
				o.WriteBytes, _ = strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			}
		}
	}
	if raw, ok := readTrim("/proc/self/stat"); ok {
		if i := strings.LastIndex(raw, ")"); i >= 0 && i+2 < len(raw) {
			f := strings.Fields(raw[i+2:])
			// utime/stime 是第 14/15 字段，去掉 pid/comm/state 后索引 11/12。
			if len(f) > 12 {
				ut, _ := strconv.ParseInt(f[11], 10, 64)
				st, _ := strconv.ParseInt(f[12], 10, 64)
				o.CPUMs = (ut + st) * 1000 / 100 // USER_HZ 固定为 100
			}
		}
	}
	return o
}
