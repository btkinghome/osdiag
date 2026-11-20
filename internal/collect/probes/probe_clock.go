package probes

import (
	"context"
	"fmt"
	"math"
	"time"

	"luanbird/osdiag/internal/collect"
)

const (
	clockSamples       = 3
	clockSampleSpacing = 10 * time.Millisecond
	// clockDriftThresholdUS：REALTIME 与 MONOTONIC 增量差的判定阈值（50ms）。
	clockDriftThresholdUS = 50000.0
)

// clockPair 是一次同时读到的两种时钟读数（纳秒）。
type clockPair struct {
	RealNS int64
	MonoNS int64
	Source string
}

// probeClock 采样 REALTIME/MONOTONIC 的增量差来观察墙钟漂移或步进。
// ok 表示采样动作成功；漂移判定写入 metrics，不据此推断系统故障。
func probeClock(ctx context.Context, _ *collect.Env) outcome {
	r := newResult()
	pairs := make([]clockPair, 0, clockSamples)
	for i := 0; i < clockSamples; i++ {
		if i > 0 {
			timer := time.NewTimer(clockSampleSpacing)
			select {
			case <-ctx.Done():
				timer.Stop()
				markFail(r, errnoOf(ctx.Err()), "clock 采样被中断: "+ctx.Err().Error())
				return outcome{res: r}
			case <-timer.C:
			}
		}
		opStart := time.Now()
		p, err := readClockPair()
		if err != nil {
			markFail(r, errnoOf(err), "clock_gettime 失败: "+err.Error())
			return outcome{res: r}
		}
		r.Samples = append(r.Samples, sample(opStart, p.Source, time.Since(opStart), -1, ""))
		pairs = append(pairs, p)
	}

	var maxAbs, sumAbs float64
	monotonicOK := true
	for i := 1; i < len(pairs); i++ {
		dr := float64(pairs[i].RealNS - pairs[i-1].RealNS)
		dm := float64(pairs[i].MonoNS - pairs[i-1].MonoNS)
		d := math.Abs(dr - dm)
		if d > maxAbs {
			maxAbs = d
		}
		sumAbs += d
		if pairs[i].MonoNS <= pairs[i-1].MonoNS {
			monotonicOK = false
		}
	}
	meanAbs := 0.0
	if len(pairs) > 1 {
		meanAbs = sumAbs / float64(len(pairs)-1)
	}
	maxUS := maxAbs / 1000
	meanUS := meanAbs / 1000

	last := pairs[len(pairs)-1]
	r.Metrics = map[string]any{
		"samples":            len(pairs),
		"interval_ms":        int(clockSampleSpacing / time.Millisecond),
		"max_drift_us":       round3(maxUS),
		"mean_abs_drift_us":  round3(meanUS),
		"threshold_drift_us": clockDriftThresholdUS,
		"drift_ok":           maxUS < clockDriftThresholdUS,
		"ok_condition":       fmt.Sprintf("max_drift_us<%g", clockDriftThresholdUS),
		"monotonic_ok":       monotonicOK,
		"realtime_ns":        last.RealNS,
		"monotonic_ns":       last.MonoNS,
		"clock_source":       last.Source,
	}
	return outcome{res: r}
}
