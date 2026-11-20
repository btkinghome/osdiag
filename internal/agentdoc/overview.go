package agentdoc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
	"luanbird/osdiag/internal/pack"
)

// writeOverview 用本轮实际产物生成一页纸入口，替换 meta 阶段的占位内容。
// 只做汇总与指针，不做研判、不给严重性。
func writeOverview(env *collect.Env) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# 诊断周期概览\n\n")
	fmt.Fprintf(&b, "- 运行：`%s`，profile `%s`\n", env.RunID, env.Profile)
	fmt.Fprintf(&b, "- 窗口：%s ~ %s（%d s）\n",
		env.Start.UTC().Format("2006-01-02T15:04:05Z"),
		env.Deadlin().UTC().Format("2006-01-02T15:04:05Z"), int64(env.Duration.Seconds()))
	fmt.Fprintf(&b, "- 主机：%s，%s，内核 `%s`（%s），%d vCPU / %s，环境 `%s`\n",
		env.Host.Hostname, env.Host.OS.PrettyName, env.Host.Kernel, env.Host.Arch,
		env.Host.NCPU, humanBytes(env.Host.MemBytes), env.Host.Env)
	b.WriteString("\n本文件只是阅读入口，不是结论。证据以 `manifest.json`、`metrics/`、`logs/`、`probes/` 中的原始记录为准。\n")

	var streams model.StreamIndex
	if err := readJSON(env.Path("metrics/streams.json"), &streams); err == nil && len(streams.Streams) > 0 {
		b.WriteString("\n## 指标流\n\n| 流 | 行数 | 间隔 | quality | 说明 |\n|---|---:|---:|---|---|\n")
		total := 0
		for _, s := range streams.Streams {
			total += s.Rows
			q := "ok"
			if len(s.Quality) > 0 {
				q = strings.Join(s.Quality, ",")
			}
			fmt.Fprintf(&b, "| `%s` | %d | %dms | %s | %s |\n", s.ID, s.Rows, s.IntervalMS, q, s.Notes)
		}
		fmt.Fprintf(&b, "\n共 %d 条流、%d 行采样。行数为 0 且 quality=unsupported 表示本机不支持该指标，不是“正常”。\n", len(streams.Streams), total)
	}

	var logs model.LogsIndex
	if err := readJSON(env.Path("logs/index.json"), &logs); err == nil {
		var bytes int64
		for _, f := range logs.Files {
			bytes += f.Bytes
		}
		b.WriteString("\n## 日志\n\n")
		fmt.Fprintf(&b, "- 文件 %d 个，共 %s，字面模式命中 %d 类\n", len(logs.Files), humanBytes(uint64(bytes)), len(logs.Patterns))
		if logs.Journal != nil {
			fmt.Fprintf(&b, "- journal：present=%t，parsed=%t", logs.Journal.Present, logs.Journal.Parsed)
			if logs.Journal.Gap != "" {
				fmt.Fprintf(&b, "，gap=`%s`", logs.Journal.Gap)
			}
			b.WriteString("\n")
		}
		if len(logs.Patterns) > 0 {
			b.WriteString("\n| 类别 | 次数 | 首次 | 最近 |\n|---|---:|---|---|\n")
			for _, p := range logs.Patterns {
				fmt.Fprintf(&b, "| `%s` | %d | %s | %s |\n", p.Category, p.Count, dash(p.FirstTS), dash(p.LastTS))
			}
			b.WriteString("\n次数只是字面出现次数，不代表故障。样本行与行号见 `logs/index.json`。\n")
		}
	}

	probes := loadProbes(env.Path("probes"))
	if len(probes) > 0 {
		var ok, skipped, failed int
		for _, p := range probes {
			switch {
			case p.Skipped:
				skipped++
			case p.OK:
				ok++
			default:
				failed++
			}
		}
		sort.Slice(probes, func(i, j int) bool {
			if probes[i].Level != probes[j].Level {
				return probes[i].Level < probes[j].Level
			}
			return probes[i].Probe < probes[j].Probe
		})
		b.WriteString("\n## 主动探测\n\n")
		fmt.Fprintf(&b, "- 共 %d 项：ok=%d，skipped=%d，ok:false=%d\n", len(probes), ok, skipped, failed)
		b.WriteString("\n| 探测 | 级别 | 结果 | 备注 |\n|---|---|---|---|\n")
		for _, p := range probes {
			res := "ok"
			note := ""
			switch {
			case p.Skipped:
				res = "skipped"
				note = p.SkipReason
			case !p.OK:
				res = "ok:false"
				if p.Error != nil {
					note = fmt.Sprintf("errno=%s %s", p.Error.Errno, p.Error.Msg)
				}
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", p.Probe, p.Level, res, note)
		}
		b.WriteString("\n`ok:false` 只表示该动作返回的 errno；`skipped` 表示未执行，两者都不能外推为系统故障。\n")
	}

	var top model.TopSummary
	if err := readJSON(env.Path("top/summary.json"), &top); err == nil {
		b.WriteString("\n## 进程\n\n")
		fmt.Fprintf(&b, "- 进程 %d，线程 %d，内核线程 %d，zombie %d，D-state %d，监听 TCP socket %d\n",
			top.Counts.Processes, top.Counts.Threads, top.Counts.KernelThreads,
			top.Counts.Zombies, top.Counts.DState, top.Counts.ListeningSockets)
		if len(top.DState) > 0 {
			fmt.Fprintf(&b, "- D-state 进程 %d 个，清单见 `top/summary.json`\n", len(top.DState))
		}
	}

	errs := env.Errors()
	markers := env.Markers()
	b.WriteString("\n## 采集缺口\n\n")
	fmt.Fprintf(&b, "- 采集器报错 %d 条，marker %d 条（详见 `manifest.json`）\n", len(errs), len(markers))
	for i, e := range errs {
		if i >= 10 {
			fmt.Fprintf(&b, "- 其余 %d 条见 manifest\n", len(errs)-10)
			break
		}
		fmt.Fprintf(&b, "- `%s`: %s\n", e.Collector, truncate(e.Msg, 200))
	}

	b.WriteString("\n## 阅读顺序\n\n")
	b.WriteString("1. `manifest.json`：status、coverage、errors、capabilities\n")
	b.WriteString("2. `catalog.json`：按 priority 选择要看的证据\n")
	b.WriteString("3. `agent/reading-guide.md`：完整阅读规则与判读边界\n")
	b.WriteString("4. `metrics/streams.json` + `agent/field-dictionary.json` → `derived/` → 目标流\n")
	b.WriteString("5. `logs/index.json` → 命中行上下文；`probes/runlog.jsonl` + `probes/*.json`\n")
	b.WriteString("6. `hardware/`、`top/`、`meta/`\n")

	return pack.WriteText(env.Path("overview.md"), b.String())
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func loadProbes(dir string) []model.ProbeResult {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	out := make([]model.ProbeResult, 0, len(paths))
	for _, p := range paths {
		var r model.ProbeResult
		if err := readJSON(p, &r); err == nil {
			out = append(out, r)
		}
	}
	return out
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
