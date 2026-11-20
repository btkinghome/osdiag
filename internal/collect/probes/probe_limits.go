package probes

import (
	"context"
	"math"
	"syscall"
	"time"

	"luanbird/osdiag/internal/collect"
)

// limitReading 是一次 getrlimit(2) 的结果；Errno 非空表示调用失败。
type limitReading struct {
	Name     string
	Resource int
	Cur      uint64
	Max      uint64
	Errno    string
	Dur      time.Duration
}

// limitVerdict 是限额判定结果，供 metrics 使用。
type limitVerdict struct {
	Name      string
	Soft      int64
	Hard      int64
	Unlimited bool
	Adequate  bool
	MinSoft   int64
}

// limitSpecs 列出要采集的限额与推荐软下限（字节或数量）。
func limitSpecs() []struct {
	name     string
	resource int
	minSoft  uint64
} {
	return []struct {
		name     string
		resource int
		minSoft  uint64
	}{
		{"nofile", syscall.RLIMIT_NOFILE, 1024},
		{"nproc", sysRLIMITNPROC, 256},
		{"as", syscall.RLIMIT_AS, 512 << 20},
		{"core", syscall.RLIMIT_CORE, 0},
	}
}

func readLimits() []limitReading {
	specs := limitSpecs()
	out := make([]limitReading, 0, len(specs))
	for _, spec := range specs {
		var rl syscall.Rlimit
		t0 := time.Now()
		err := syscall.Getrlimit(spec.resource, &rl)
		reading := limitReading{Name: spec.name, Resource: spec.resource, Dur: time.Since(t0)}
		if err != nil {
			reading.Errno = errnoOf(err)
		} else {
			reading.Cur, reading.Max = rl.Cur, rl.Max
		}
		out = append(out, reading)
	}
	return out
}

// isUnlimited 兼容 Linux 的 ULONG_MAX 与 BSD 的 0x7fff... 两种表示。
func isUnlimited(v uint64) bool {
	return v == ^uint64(0) || v == math.MaxInt64
}

// judgeLimit 按推荐软下限判定单个限额是否充足；无限额视为充足。
func judgeLimit(name string, cur uint64) (adequate bool, minSoft int64) {
	for _, spec := range limitSpecs() {
		if spec.name != name {
			continue
		}
		if isUnlimited(cur) {
			return true, int64(spec.minSoft)
		}
		return cur >= spec.minSoft, int64(spec.minSoft)
	}
	return true, 0
}

// evaluateLimits 汇总判定；第二个返回值要求所有调用成功且判定充足。
func evaluateLimits(readings []limitReading) ([]limitVerdict, bool) {
	verdicts := make([]limitVerdict, 0, len(readings))
	allOK := true
	for _, rd := range readings {
		v := limitVerdict{Name: rd.Name, Soft: encodeLimit(rd.Cur), Hard: encodeLimit(rd.Max), Unlimited: isUnlimited(rd.Cur)}
		if rd.Errno != "" {
			allOK = false
			v.Adequate = false
			verdicts = append(verdicts, v)
			continue
		}
		v.Adequate, v.MinSoft = judgeLimit(rd.Name, rd.Cur)
		if !v.Adequate {
			allOK = false
		}
		verdicts = append(verdicts, v)
	}
	return verdicts, allOK
}

// encodeLimit 把 uint64 限额转成 JSON 友好数值，无限额记为 -1。
func encodeLimit(v uint64) int64 {
	if isUnlimited(v) {
		return -1
	}
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// probeLimits 读取 NOFILE/NPROC/AS/CORE 四个限额并写数值 metrics。
func probeLimits(ctx context.Context, _ *collect.Env) outcome {
	r := newResult()
	if err := ctx.Err(); err != nil {
		markFail(r, errnoOf(err), "limits 探测开始前已取消: "+err.Error())
		return outcome{res: r}
	}
	readings := readLimits()
	for _, rd := range readings {
		r.Samples = append(r.Samples, sample(time.Now(), "getrlimit:"+rd.Name, rd.Dur, -1, rd.Errno))
	}
	verdicts, adequate := evaluateLimits(readings)
	metrics := map[string]any{
		"limits_adequate":       adequate,
		"ok_condition":          "soft>=recommended 或 unlimited",
		"getrlimit_error_count": 0,
	}
	errCount := 0
	firstErr := ""
	firstErrName := ""
	for _, rd := range readings {
		if rd.Errno != "" {
			errCount++
			if firstErr == "" {
				firstErr, firstErrName = rd.Errno, rd.Name
			}
		}
	}
	metrics["getrlimit_error_count"] = errCount
	for _, v := range verdicts {
		metrics[v.Name+"_soft"] = v.Soft
		metrics[v.Name+"_hard"] = v.Hard
		metrics[v.Name+"_unlimited"] = v.Unlimited
		metrics[v.Name+"_adequate"] = v.Adequate
	}
	r.Metrics = metrics
	if firstErr != "" {
		markFail(r, firstErr, "getrlimit("+firstErrName+") 调用失败")
	}
	return outcome{res: r}
}
