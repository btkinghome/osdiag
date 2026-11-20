package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
	"luanbird/osdiag/internal/pack"
	"luanbird/osdiag/internal/runid"
	"luanbird/osdiag/internal/sysinfo"
	"luanbird/osdiag/internal/version"
)

type runOptions struct {
	profile       string
	duration      int
	workdir       string
	out           string
	allowHeavy    bool
	allowExternal bool
	noProbe       bool
}

var profileDurations = map[string]int{
	"quick":    30,
	"standard": 120,
	"deep":     600,
}

func runCmd(args []string) int {
	fs, o := newRunFlags()
	if err := fs.Parse(args); err != nil {
		return 5
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "run 不接受位置参数: %v\n", fs.Args())
		return 5
	}
	def, ok := profileDurations[o.profile]
	if !ok {
		fmt.Fprintf(os.Stderr, "未知 profile: %s（可选 quick/standard/deep）\n", o.profile)
		return 5
	}
	if o.duration < 0 {
		fmt.Fprintln(os.Stderr, "--duration 不能为负")
		return 5
	}
	if o.duration == 0 {
		o.duration = def
	}
	if o.out == "" {
		o.out = filepath.Join(o.workdir, "out")
	}

	start := time.Now().UTC()
	id := runid.New(start)
	stage := filepath.Join(o.workdir, "runs", id, "stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建工作目录失败: %v\n", err)
		return 3
	}
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建输出目录失败: %v\n", err)
		return 3
	}

	lockPath := filepath.Join(o.workdir, ".lock")
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开锁文件失败: %v\n", err)
		return 3
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			fmt.Fprintln(os.Stderr, "已有 osdiag 实例在运行（同一 workdir）")
			return 5
		}
		fmt.Fprintf(os.Stderr, "加锁失败: %v\n", err)
		return 3
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	res, err := collect.Collect(stage)
	if err != nil {
		fmt.Fprintf(os.Stderr, "采集失败: %v\n", err)
		return 3
	}

	interrupted := false
	select {
	case <-sigCh:
		interrupted = true
	default:
	}

	// catalog 在内容文件之后写；写完后重新扫描，使 artifacts 覆盖 catalog。
	arts, err := pack.Scan(stage)
	if err != nil {
		fmt.Fprintf(os.Stderr, "扫描产物失败: %v\n", err)
		return 6
	}
	if err := pack.WriteJSON(filepath.Join(stage, "catalog.json"), buildCatalog(arts)); err != nil {
		fmt.Fprintf(os.Stderr, "写 catalog 失败: %v\n", err)
		return 6
	}
	arts, err = pack.Scan(stage)
	if err != nil {
		fmt.Fprintf(os.Stderr, "扫描产物失败: %v\n", err)
		return 6
	}

	end := time.Now().UTC()
	status, code := "ok", 0
	if len(res.Errors) > 0 {
		status, code = "partial", 2
	}
	if interrupted {
		status, code = "interrupted", 4
	}

	manifest := &model.Manifest{
		SchemaVersion: model.SchemaVersion,
		Tool: model.Tool{
			Name:    "osdiag",
			Version: version.Version,
			Go:      runtime.Version(),
			Build:   version.Build,
			Commit:  version.Commit,
		},
		Run: model.Run{
			ID:         id,
			Profile:    o.profile,
			Start:      start.Format(time.RFC3339),
			End:        end.Format(time.RFC3339),
			DurationS:  int(end.Sub(start).Seconds()),
			Status:     status,
			ExitCode:   code,
			Args:       append([]string{"run"}, args...),
			ConfigHash: configHash(o),
		},
		Host: res.Host,
		ProbePolicy: model.ProbePolicy{
			AllowHeavy:           o.allowHeavy,
			AllowExternal:        o.allowExternal,
			Enabled:              []string{},
			SkippedDueToPressure: []string{},
		},
		Coverage: model.Coverage{
			ExpectedSamples: 0,
			ActualSamples:   0,
			Ratio:           0,
			Gaps:            []model.Gap{},
		},
		Capabilities: res.Capabilities,
		Artifacts:    arts,
		Errors:       res.Errors,
		Overhead:     sysinfo.SelfOverhead(),
		Markers: []model.Marker{
			{TS: start.Format(time.RFC3339), Kind: "phase", Message: "M0 基础信息采集开始"},
			{TS: end.Format(time.RFC3339), Kind: "phase", Message: "M0 基础信息采集完成"},
		},
		NetTargets: []model.NetTarget{},
		Counts:     &model.Counts{KernelModules: len(sysinfo.Modules())},
	}
	if manifest.Errors == nil {
		manifest.Errors = []model.RunError{}
	}
	if manifest.Capabilities == nil {
		manifest.Capabilities = []model.Capability{}
	}

	if err := pack.WriteJSON(filepath.Join(stage, "manifest.json"), manifest); err != nil {
		fmt.Fprintf(os.Stderr, "写 manifest 失败: %v\n", err)
		return 6
	}
	if err := pack.WriteSums(stage); err != nil {
		fmt.Fprintf(os.Stderr, "写校验和失败: %v\n", err)
		return 6
	}

	archive := filepath.Join(o.out, fmt.Sprintf("osdiag_%s_%s.tar.gz", sanitizeHost(res.Host.Hostname), id))
	if err := pack.TarGz(stage, archive); err != nil {
		fmt.Fprintf(os.Stderr, "打包失败: %v\n", err)
		return 6
	}
	abs, err := filepath.Abs(archive)
	if err != nil {
		abs = archive
	}
	fmt.Printf("run=%s status=%s artifacts=%d\n", id, status, len(arts))
	fmt.Printf("ARCHIVE=%s\n", abs)
	return code
}

func configHash(o *runOptions) string {
	cfg := struct {
		Profile       string   `json:"profile"`
		Duration      int      `json:"duration"`
		AllowHeavy    bool     `json:"allow_heavy"`
		AllowExternal bool     `json:"allow_external"`
		NoProbe       bool     `json:"no_probe"`
		Args          []string `json:"args"`
	}{o.profile, o.duration, o.allowHeavy, o.allowExternal, o.noProbe, os.Args[1:]}
	b, _ := json.Marshal(cfg)
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

var catalogDescriptions = map[string]string{
	"README.md":           "包说明与安全声明",
	"overview.md":         "一页纸概览：主机、内核、环境、包内容",
	"meta/os-release.txt": "发行版标识原文",
	"meta/host.json":      "主机身份、内核、CPU/内存、虚拟化环境",
	"meta/kernel.json":    "内核版本、cmdline、tainted、boot_id",
	"meta/cpu.json":       "CPU 型号、核数、hypervisor 标志",
	"meta/meminfo.json":   "内存与交换分区总量/可用量",
	"meta/sysctl.json":    "与诊断相关的 sysctl 键值",
	"meta/selinux.json":   "SELinux 状态",
	"meta/cgroup.json":    "cgroup 版本",
	"meta/modules.txt":    "已加载内核模块名",
	"hardware/dmi.json":   "DMI/SMBIOS 只读字段",
	"catalog.json":        "产物目录（本文件）",
}

func buildCatalog(arts []model.Artifact) model.Catalog {
	entries := make([]model.CatalogEntry, 0, len(arts))
	for _, a := range arts {
		desc, ok := catalogDescriptions[a.Path]
		if !ok {
			desc = "诊断产物"
		}
		prio := 2
		if a.Path == "overview.md" || a.Path == "meta/host.json" {
			prio = 1
		}
		entries = append(entries, model.CatalogEntry{
			Path:        a.Path,
			Kind:        a.Kind,
			Priority:    prio,
			Description: desc,
			Bytes:       a.Bytes,
			Rows:        a.Rows,
			Schema:      a.Schema,
		})
	}
	return model.Catalog{SchemaVersion: model.SchemaVersion, Entries: entries}
}
