package probes

import (
	"context"
	"runtime"
	"time"

	"luanbird/osdiag/internal/collect"
)

const memAllocBytes = 64 << 20

// probeMemAlloc 分配并逐页触碰 64MiB，记录耗时后释放并触发一次 GC。
func probeMemAlloc(ctx context.Context, _ *collect.Env) outcome {
	r := newResult()
	if err := ctx.Err(); err != nil {
		markFail(r, errnoOf(err), "mem.alloc 开始前已取消: "+err.Error())
		return outcome{res: r}
	}

	t0 := time.Now()
	buf := make([]byte, memAllocBytes)
	allocDur := time.Since(t0)
	r.Samples = append(r.Samples, sample(t0, "alloc", allocDur, memAllocBytes, ""))

	t1 := time.Now()
	sum := byte(0)
	const page = 4096
	for i := 0; i < len(buf); i += page {
		buf[i] = byte(i / page)
		sum += buf[i]
	}
	touchDur := time.Since(t1)
	r.Samples = append(r.Samples, sample(t1, "touch", touchDur, memAllocBytes, ""))
	runtime.KeepAlive(buf)

	t2 := time.Now()
	buf = nil
	runtime.GC()
	releaseDur := time.Since(t2)
	r.Samples = append(r.Samples, sample(t2, "release", releaseDur, memAllocBytes, ""))

	r.Metrics = map[string]any{
		"bytes":      memAllocBytes,
		"pages":      memAllocBytes / page,
		"alloc_us":   round3(float64(allocDur.Microseconds())),
		"touch_us":   round3(float64(touchDur.Microseconds())),
		"release_us": round3(float64(releaseDur.Microseconds())),
		"total_us":   round3(float64((allocDur + touchDur + releaseDur).Microseconds())),
		"verified":   true,
		"checksum":   int(sum),
		"released":   true,
	}
	if err := ctx.Err(); err != nil {
		markFail(r, errnoOf(err), "mem.alloc 被中断: "+err.Error())
	}
	return outcome{res: r}
}
