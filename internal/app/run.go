package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"

	"luanbird/osdiag/internal/agentdoc"
	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/collect/hw"
	"luanbird/osdiag/internal/collect/logs"
	"luanbird/osdiag/internal/collect/metrics"
	"luanbird/osdiag/internal/collect/probes"
	"luanbird/osdiag/internal/collect/proc"
	"luanbird/osdiag/internal/derive"
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
	journalFile   string
	withEnviron   bool
}

var profileDurations = map[string]int{
	"quick":    30,
	"standard": 120,
	"deep":     600,
}

func newRunFlags() (*flag.FlagSet, *runOptions) {
	o := &runOptions{}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.StringVar(&o.profile, "profile", "standard", "诊断档位：quick/standard/deep")
	fs.IntVar(&o.duration, "duration", 0, "采样秒数（覆盖 profile 默认值）")
	fs.StringVar(&o.workdir, "workdir", "/var/lib/osdiag", "工作目录")
	fs.StringVar(&o.out, "out", "", "压缩包输出目录（默认 <workdir>/out）")
	fs.BoolVar(&o.allowHeavy, "allow-heavy", false, "允许 P1 探测（沙箱写、短时 CPU/内存压力、ICMP）")
	fs.BoolVar(&o.allowExternal, "allow-external", false, "允许 P2 外网探测")
	fs.BoolVar(&o.noProbe, "no-probe", false, "关闭全部主动探测")
	fs.StringVar(&o.journalFile, "journal-file", "", "导入 journalctl -o export 文本（可重复用逗号分隔）")
	fs.BoolVar(&o.withEnviron, "with-environ", false, "采集进程环境变量（默认不采集）")
	return fs, o
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var interrupted atomic.Bool
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1)
	defer signal.Stop(sigCh)
	go func() {
		for sig := range sigCh {
			if sig == syscall.SIGUSR1 {
				fmt.Fprintf(os.Stderr, "marker: SIGUSR1 at %s\n", time.Now().UTC().Format(time.RFC3339))
				continue
			}
			interrupted.Store(true)
			cancel()
			return
		}
	}()

	res, err := collect.Collect(stage)
	if err != nil {
		fmt.Fprintf(os.Stderr, "采集失败: %v\n", err)
		return 3
	}

	env := collect.NewEnv(stage, id, o.workdir, o.profile, start, time.Duration(o.duration)*time.Second)
	env.Host = res.Host
	env.AllowHeavy = o.allowHeavy
	env.AllowExtern = o.allowExternal
	env.NoProbe = o.noProbe
	env.JournalFile = o.journalFile
	env.WithEnviron = o.withEnviron
	env.AddMarker("phase", "run 开始（profile=%s duration=%ds）", o.profile, o.duration)

	runStep := func(name string, fn func() error) {
		if err := fn(); err != nil {
			env.AddError(name, "%v", err)
		}
	}

	runStep("proc", func() error { return proc.Begin(ctx, env) })
	runStep("metrics", func() error { return metrics.Run(ctx, env) })

	if !interrupted.Load() {
		runStep("logs", func() error { return logs.Run(ctx, env) })
		runStep("hardware", func() error { return hw.Run(ctx, env) })
		runStep("proc", func() error { return proc.End(ctx, env) })
		runStep("probes", func() error { return probes.Run(ctx, env) })
	} else {
		runStep("proc", func() error { return proc.End(ctx, env) })
	}
	ensureInterruptedArtifacts(env, stage)
	runStep("derive", func() error { return derive.Run(env) })
	runStep("agentdoc", func() error { return agentdoc.Run(env) })

	if interrupted.Load() {
		env.AddMarker("signal", "收到中断信号，已按已有产物收尾")
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
	if len(env.Errors()) > 0 {
		status, code = "partial", 2
	}
	if interrupted.Load() {
		status, code = "interrupted", 4
	}

	env.AddMarker("phase", "run 结束（status=%s）", status)
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
			Enabled:              env.EnabledProbes(),
			SkippedDueToPressure: env.SkippedProbes(),
		},
		Coverage:     env.Coverage(),
		Capabilities: res.Capabilities,
		Artifacts:    arts,
		Errors:       env.Errors(),
		Overhead:     sysinfo.SelfOverhead(),
		Markers:      env.Markers(),
		NetTargets:   env.NetTargets(),
		Counts:       countsPtr(env.Counts(), len(sysinfo.Modules())),
	}
	if manifest.Errors == nil {
		manifest.Errors = []model.RunError{}
	}
	if manifest.Capabilities == nil {
		manifest.Capabilities = []model.Capability{}
	}
	if manifest.Markers == nil {
		manifest.Markers = []model.Marker{}
	}
	if manifest.NetTargets == nil {
		manifest.NetTargets = []model.NetTarget{}
	}
	if manifest.ProbePolicy.Enabled == nil {
		manifest.ProbePolicy.Enabled = []string{}
	}
	if manifest.ProbePolicy.SkippedDueToPressure == nil {
		manifest.ProbePolicy.SkippedDueToPressure = []string{}
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

// ensureInterruptedArtifacts 保证中断时也留下可校验的最小占位文件，
// 避免包因为缺少 logs/index.json 或 probes/runlog.jsonl 而无法通过 schema 校验。
func ensureInterruptedArtifacts(env *collect.Env, stage string) {
	logsIndex := filepath.Join(stage, "logs", "index.json")
	if _, err := os.Stat(logsIndex); err != nil {
		_ = os.MkdirAll(filepath.Dir(logsIndex), 0o755)
		_ = pack.WriteJSON(logsIndex, &model.LogsIndex{
			SchemaVersion: model.SchemaVersion,
			Files:         []model.LogFile{},
			Patterns:      []model.LogPattern{},
		})
	}
	runlog := filepath.Join(stage, "probes", "runlog.jsonl")
	if _, err := os.Stat(runlog); err != nil {
		_ = os.MkdirAll(filepath.Dir(runlog), 0o755)
		_ = os.WriteFile(runlog, []byte{}, 0o644)
	}
}

func countsPtr(c model.Counts, modules int) *model.Counts {
	c.KernelModules = modules
	return &c
}

func configHash(o *runOptions) string {
	cfg := struct {
		Profile       string   `json:"profile"`
		Duration      int      `json:"duration"`
		AllowHeavy    bool     `json:"allow_heavy"`
		AllowExternal bool     `json:"allow_external"`
		NoProbe       bool     `json:"no_probe"`
		JournalFile   string   `json:"journal_file"`
		WithEnviron   bool     `json:"with_environ"`
		Args          []string `json:"args"`
	}{o.profile, o.duration, o.allowHeavy, o.allowExternal, o.noProbe, o.journalFile, o.withEnviron, os.Args[1:]}
	b, _ := json.Marshal(cfg)
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

var catalogDescriptions = map[string]string{
	"README.md":                     "包说明与安全声明",
	"overview.md":                   "一页纸概览：主机、内核、环境、包内容",
	"meta/os-release.txt":           "发行版标识原文",
	"meta/host.json":                "主机身份、内核、CPU/内存、虚拟化环境",
	"meta/kernel.json":              "内核版本、cmdline、tainted、boot_id",
	"meta/cpu.json":                 "CPU 型号、核数、hypervisor 标志",
	"meta/meminfo.json":             "内存与交换分区总量/可用量",
	"meta/sysctl.json":              "与诊断相关的 sysctl 键值",
	"meta/selinux.json":             "SELinux 状态",
	"meta/cgroup.json":              "cgroup 版本",
	"meta/modules.txt":              "已加载内核模块名",
	"hardware/dmi.json":             "DMI/SMBIOS 只读字段",
	"metrics/streams.json":          "指标流清单与数据质量",
	"derived/field-stats.json":      "各流数值分布（min/max/p50/p95/p99）",
	"derived/deltas.json":           "计数器 T0→T1 增量与速率",
	"derived/formulas.json":         "派生指标公式与单位",
	"agent/field-dictionary.json":   "字段字典：类型、单位、counter/gauge",
	"agent/reading-guide.md":        "给 Agent 的阅读顺序与判读边界",
	"agent/glossary.json":           "术语表",
	"agent/schema-manifest.json":    "包内 schema 清单",
	"logs/index.json":               "日志文件与字面模式命中索引",
	"top/summary.json":              "进程表高信号摘要",
	"top/processes.jsonl":           "进程明细（资源与身份证据）",
	"probes/runlog.jsonl":           "探测生命周期日志",
	"catalog.json":                  "产物目录（本文件）",
}

func buildCatalog(arts []model.Artifact) model.Catalog {
	entries := make([]model.CatalogEntry, 0, len(arts))
	for _, a := range arts {
		desc, ok := catalogDescriptions[a.Path]
		if !ok {
			desc = "诊断产物"
		}
		prio := 2
		switch {
		case a.Path == "overview.md" || a.Path == "meta/host.json":
			prio = 1
		case a.Path == "metrics/streams.json" || a.Path == "logs/index.json" || a.Path == "top/summary.json":
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
