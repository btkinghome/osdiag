// Package probes 执行 M3 主动探测：时间/系统调用/limits/loopback/本机解析（P0），
// 沙箱写、CPU/内存压力、ICMP（P1）与外网 DNS/TCP/ICMP（P2）。
//
// 探测只验证“系统还能不能做这个动作”，输出耗时与 errno；不推断系统故障。
// 默认只跑 P0：P1 需要 --allow-heavy，P2 需要 --allow-heavy + --allow-external。
// 每个探测独立超时；未开启或压力保护跳过的探测同样落盘结果与 runlog skip 事件。
package probes

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
	"luanbird/osdiag/internal/pack"
)

const (
	// probeTimeout 是单项探测的硬上限（附录 B：单探测 5s）。
	probeTimeout = 5 * time.Second
	// budgetRatio 是探测总量占诊断窗口的比例（设计稿：≤25%）。
	budgetRatio = 4
	// minBudget 保证短窗口下 P0 仍有机会完成。
	minBudget = 5 * time.Second
)

// outcome 是单项探测的返回值。cleanup 非空时由编排器补一条 cleanup runlog。
type outcome struct {
	res     *model.ProbeResult
	cleanup string
}

type probeFunc func(context.Context, *collect.Env) outcome

type probeSpec struct {
	id    string
	level string
	fn    probeFunc
}

// probeSpecs 按 P0→P1→P2 顺序排列，保证轻量探测先完成。
func probeSpecs() []probeSpec {
	return []probeSpec{
		{id: "time.clock", level: "P0", fn: probeClock},
		{id: "syscall.basic", level: "P0", fn: probeSyscall},
		{id: "limits.self", level: "P0", fn: probeLimits},
		{id: "net.loopback", level: "P0", fn: probeNetLoopback},
		{id: "dns.localhost", level: "P0", fn: probeDNSLocalhost},

		{id: "fs.sandbox", level: "P1", fn: probeFSSandbox},
		{id: "cpu.burn", level: "P1", fn: probeCPUBurn},
		{id: "mem.alloc", level: "P1", fn: probeMemAlloc},
		{id: "net.icmp_gateway", level: "P1", fn: probeICMPGateway},

		{id: "dns.external", level: "P2", fn: probeDNSExternal},
		{id: "net.tcp_external", level: "P2", fn: probeTCPExternal},
		{id: "net.icmp_external", level: "P2", fn: probeICMPExternal},
	}
}

// Run 执行探测并写出 probes/<id>.json 与 probes/runlog.jsonl。
// 单项探测失败只落盘为 ok=false；只有输出文件无法写入时才返回错误。
func Run(ctx context.Context, env *collect.Env) (retErr error) {
	log, err := openEventLog(env.Path("probes/runlog.jsonl"))
	if err != nil {
		return fmt.Errorf("创建 probes/runlog.jsonl: %w", err)
	}
	defer func() {
		if cerr := log.close(); cerr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("关闭 probes/runlog.jsonl: %w", cerr))
		}
	}()

	budget := env.Duration / budgetRatio
	if budget < minBudget {
		budget = minBudget
	}
	bctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	// 压力保护只与 P1/P2 有关；未开启重项时不必读 /proc。
	pressureOn := env.AllowHeavy && !env.NoProbe
	var press pressureSample
	if pressureOn {
		press = readPressure()
	}
	cpus := ncpu(env)

	var errs []error
	appendErr := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	for _, spec := range probeSpecs() {
		reason, skip, dueToPressure := skipReason(bctx, env, spec, press, pressureOn, cpus)
		if skip {
			start := time.Now()
			res := skipResult(reason)
			finalize(res, spec.id, spec.level, start)
			appendErr(writeResult(env, res))
			appendErr(log.emit(skipEvent(spec.id, spec.level, reason, res.DurationMS)))
			if dueToPressure {
				env.SkipProbe(spec.id, reason)
			}
			continue
		}

		env.EnableProbe(spec.id)
		start := time.Now()
		appendErr(log.emit(startEvent(spec.id, spec.level)))

		out := execute(bctx, env, spec)
		res := out.res
		if res == nil {
			res = failResult("EUNKNOWN", "probe returned nil result")
		}
		finalize(res, spec.id, spec.level, start)
		appendErr(writeResult(env, res))

		if res.Skipped {
			appendErr(log.emit(skipEvent(spec.id, spec.level, res.SkipReason, res.DurationMS)))
			continue
		}
		if !res.OK {
			detail := "probe failed"
			if res.Error != nil {
				detail = fmt.Sprintf("errno=%s msg=%s", res.Error.Errno, res.Error.Msg)
			}
			appendErr(log.emit(errorEvent(spec.id, spec.level, detail)))
		}
		if out.cleanup != "" {
			appendErr(log.emit(cleanupEvent(spec.id, spec.level, out.cleanup)))
		}
		appendErr(log.emit(endEvent(spec.id, spec.level, res.DurationMS)))
	}

	return errors.Join(errs...)
}

// execute 在独立 goroutine 中运行单项探测，捕获 panic 与超时。
func execute(ctx context.Context, env *collect.Env, spec probeSpec) outcome {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	ch := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- outcome{res: failResult("EPANIC", fmt.Sprintf("probe panic: %v", r))}
			}
		}()
		ch <- spec.fn(pctx, env)
	}()

	select {
	case out := <-ch:
		return out
	case <-pctx.Done():
		// 探测可能恰好在超时瞬间完成，先尝试取回已完成结果。
		select {
		case out := <-ch:
			return out
		default:
		}
		if errors.Is(pctx.Err(), context.DeadlineExceeded) {
			return outcome{res: failResult("ETIMEDOUT", fmt.Sprintf("探测超过单项上限 %s", probeTimeout))}
		}
		return outcome{res: failResult("ECANCELED", "探测被取消")}
	}
}

// skipReason 判定某个探测是否因开关、压力或预算而不执行。
// 第三个返回值表示是否属于压力保护（用于 manifest.skipped_due_to_pressure）。
func skipReason(ctx context.Context, env *collect.Env, spec probeSpec, press pressureSample, pressureOn bool, cpus int) (string, bool, bool) {
	if env.NoProbe {
		return "no_probe=true：按 --no-probe 关闭全部主动探测", true, false
	}
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "探测总预算（诊断窗口的 25%）已用尽", true, false
		}
		return "探测被取消（context canceled）", true, false
	}
	switch spec.level {
	case "P1":
		if !env.AllowHeavy {
			return "allow_heavy=false：P1 需要 --allow-heavy", true, false
		}
	case "P2":
		if !env.AllowHeavy {
			return "allow_heavy=false：P2 需要 --allow-heavy 与 --allow-external", true, false
		}
		if !env.AllowExtern {
			return "allow_external=false：P2 需要 --allow-external", true, false
		}
	}
	if pressureOn && spec.level != "P0" {
		if skip, reason := evaluatePressure(press, cpus); skip {
			return "系统压力保护：" + reason, true, true
		}
	}
	return "", false, false
}

// ncpu 取主机 CPU 数；Env 未填时退回 runtime。
func ncpu(env *collect.Env) int {
	if env != nil && env.Host.NCPU > 0 {
		return env.Host.NCPU
	}
	return runtime.NumCPU()
}

// writeResult 把结果写到 probes/<probe>.json，probe 字段与文件名一致。
func writeResult(env *collect.Env, res *model.ProbeResult) error {
	if res == nil {
		return nil
	}
	path := env.Path("probes/" + res.Probe + ".json")
	if err := pack.WriteJSON(path, res); err != nil {
		return fmt.Errorf("写 %s: %w", path, err)
	}
	return nil
}

// truncate 按字符截断，避免超出 schema 的 maxLength。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// tsLayout 对齐设计稿：RFC3339 + 毫秒。
const tsLayout = "2006-01-02T15:04:05.000Z07:00"

func formatTS(t time.Time) string { return t.UTC().Format(tsLayout) }

func roundMS(d time.Duration) float64 {
	ms := float64(d) / float64(time.Millisecond)
	return float64(int64(ms*1000+0.5)) / 1000
}

// round3 保留三位小数。
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func durationPtr(ms float64) *float64 { return &ms }

func msPtr(d time.Duration) *float64 {
	v := roundMS(d)
	return &v
}

// ---- 结果构造 ----

func newResult() *model.ProbeResult {
	return &model.ProbeResult{OK: true}
}

func failResult(errno, msg string) *model.ProbeResult {
	r := newResult()
	r.OK = false
	r.Error = &model.ProbeError{Errno: errno, Msg: truncate(msg, 500)}
	return r
}

func skipResult(reason string) *model.ProbeResult {
	return &model.ProbeResult{
		OK:         false,
		Skipped:    true,
		SkipReason: truncate(reason, 300),
	}
}

func markFail(r *model.ProbeResult, errno, msg string) {
	r.OK = false
	r.Error = &model.ProbeError{Errno: errno, Msg: truncate(msg, 500)}
}

func markSkip(r *model.ProbeResult, reason string) {
	r.OK = false
	r.Skipped = true
	r.SkipReason = truncate(reason, 300)
}

// finalize 补齐 schema 公共字段并兜底 error/skip_reason 约束。
func finalize(r *model.ProbeResult, id, level string, start time.Time) {
	r.SchemaVersion = model.SchemaVersion
	r.Probe = id
	r.Level = level
	r.StartedAt = formatTS(start)
	r.DurationMS = roundMS(time.Since(start))
	if r.Skipped {
		r.OK = false
		r.Error = nil
		if r.SkipReason == "" {
			r.SkipReason = "skipped"
		}
		r.SkipReason = truncate(r.SkipReason, 300)
		return
	}
	if !r.OK {
		if r.Error == nil {
			r.Error = &model.ProbeError{Errno: "EUNKNOWN", Msg: "probe failed without error detail"}
		}
		if r.Error.Errno == "" {
			r.Error.Errno = "EUNKNOWN"
		}
		if r.Error.Msg == "" {
			r.Error.Msg = "probe failed"
		}
		r.Error.Msg = truncate(r.Error.Msg, 500)
	}
}

// sample 构造一条样本；bytes<0 表示不写 bytes。
func sample(ts time.Time, op string, d time.Duration, bytes int64, errno string) model.ProbeSample {
	s := model.ProbeSample{
		TS:         formatTS(ts),
		Op:         truncate(op, 64),
		DurationUS: float64(d) / float64(time.Microsecond),
		Errno:      errno,
	}
	if bytes >= 0 {
		b := bytes
		s.Bytes = &b
	}
	return s
}

// metricKey 把网络目标转成 metrics 键的一部分。
func metricKey(target string) string {
	out := make([]rune, 0, len(target))
	for _, r := range target {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// ensure 目录存在（probes/ 或沙箱目录）。
func ensureDir(path string) error { return os.MkdirAll(path, 0o755) }
