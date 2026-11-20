package probes

import (
	"context"
	"net"
	"strings"
	"time"

	"luanbird/osdiag/internal/collect"
)

// probeDNSLocalhost 只验证 localhost 的本机解析（通常来自 /etc/hosts 或本地 stub）。
func probeDNSLocalhost(ctx context.Context, _ *collect.Env) outcome {
	r := newResult()
	t0 := time.Now()
	hosts, err := net.DefaultResolver.LookupHost(ctx, "localhost")
	dur := time.Since(t0)
	r.Samples = append(r.Samples, sample(t0, "lookup_host", dur, -1, errnoOf(err)))
	if err != nil {
		markFail(r, errnoOf(err), "解析 localhost 失败: "+err.Error())
		return outcome{res: r}
	}
	hasLoopback := false
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
			hasLoopback = true
			break
		}
	}
	r.Metrics = map[string]any{
		"name":         "localhost",
		"host_count":   len(hosts),
		"hosts":        strings.Join(hosts, ","),
		"has_loopback": hasLoopback,
		"duration_us":  round3(float64(dur.Microseconds())),
		"resolver":     "net.DefaultResolver",
	}
	return outcome{res: r}
}
