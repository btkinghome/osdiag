package probes

import (
	"context"
	"runtime"
	"time"

	"luanbird/osdiag/internal/collect"
)

// cpuBurnSink 防止编译器把忙循环优化掉。
var cpuBurnSink uint64

const (
	cpuBurnDuration = 500 * time.Millisecond
	cpuBurnBatch    = 4096
)

// probeCPUBurn 做 500ms 单核忙循环，记录实际耗时与迭代次数。
func probeCPUBurn(ctx context.Context, _ *collect.Env) outcome {
	r := newResult()
	x := uint64(0x9e3779b97f4a7c15)
	var iters uint64
	start := time.Now()
	deadline := start.Add(cpuBurnDuration)

loop:
	for {
		for i := 0; i < cpuBurnBatch; i++ {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			iters++
		}
		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			break loop
		default:
		}
	}
	actual := time.Since(start)
	cpuBurnSink = x
	r.Samples = append(r.Samples, sample(start, "burn", actual, -1, ""))

	itersPerSec := 0.0
	if secs := actual.Seconds(); secs > 0 {
		itersPerSec = float64(iters) / secs
	}
	r.Metrics = map[string]any{
		"target_ms":          int(cpuBurnDuration / time.Millisecond),
		"actual_ms":          round3(float64(actual.Microseconds()) / 1000),
		"iterations":         iters,
		"iterations_per_sec": round3(itersPerSec),
		"cpu_count":          runtime.NumCPU(),
	}
	if err := ctx.Err(); err != nil {
		markFail(r, errnoOf(err), "cpu.burn 被中断: "+err.Error())
	}
	return outcome{res: r}
}
