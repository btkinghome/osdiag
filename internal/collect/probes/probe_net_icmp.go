package probes

import (
	"context"
	"net"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
)

const (
	icmpCount       = 3
	icmpExternalDst = "223.5.5.5"
)

// icmpAttempt 发 ICMP 并登记一个网络目标。
func icmpAttempt(ctx context.Context, env *collect.Env, dst, proto string, count int) ([]time.Duration, error) {
	ip := net.ParseIP(dst)
	if ip == nil {
		return nil, newErrnoError("EINVAL", "非法 IP 地址: "+dst)
	}
	rtts, err := icmpPing(ctx, ip, count, time.Second)
	_, avgMs, _, _ := summarizeRTT(rtts)
	var avgPtr *float64
	if len(rtts) > 0 {
		avgPtr = &avgMs
	}
	env.AddNetTarget(model.NetTarget{
		Target:    dst,
		Proto:     proto,
		Attempted: true,
		OK:        len(rtts) > 0 && err == nil,
		RTTMs:     avgPtr,
	})
	return rtts, err
}

func recordICMPSamples(r *model.ProbeResult, rtts []time.Duration) {
	for _, d := range rtts {
		r.Samples = append(r.Samples, sample(time.Now(), "icmp_echo", d, int64(len(icmpPayloadMarker)), ""))
	}
}

func icmpMetrics(dst string, count int, rtts []time.Duration) map[string]any {
	minMs, avgMs, maxMs, list := summarizeRTT(rtts)
	lossPct := 100.0 * float64(count-len(rtts)) / float64(count)
	return map[string]any{
		"target":     dst,
		"requested":  count,
		"replies":    len(rtts),
		"loss_pct":   round3(lossPct),
		"min_rtt_ms": round3(minMs),
		"avg_rtt_ms": round3(avgMs),
		"max_rtt_ms": round3(maxMs),
		"rtts_ms":    list,
	}
}

// probeICMPGateway 读取默认网关并发最多 3 个 ICMP echo。
func probeICMPGateway(ctx context.Context, env *collect.Env) outcome {
	r := newResult()
	gw, iface, err := readDefaultGateway()
	if err != nil {
		markFail(r, errnoOf(err), "读取默认网关失败: "+err.Error())
		return outcome{res: r}
	}
	rtts, pingErr := icmpAttempt(ctx, env, gw, "icmp", icmpCount)
	recordICMPSamples(r, rtts)
	m := icmpMetrics(gw, icmpCount, rtts)
	m["gateway"] = gw
	m["iface"] = iface
	r.Metrics = m
	switch {
	case pingErr != nil:
		markFail(r, errnoOf(pingErr), "ICMP 探测失败: "+pingErr.Error())
	case len(rtts) == 0:
		markFail(r, "ETIMEDOUT", "3 个 ICMP 包均未收到回显")
	}
	return outcome{res: r}
}

// probeICMPExternal 向 223.5.5.5 发最多 3 个 ICMP echo。
func probeICMPExternal(ctx context.Context, env *collect.Env) outcome {
	r := newResult()
	rtts, err := icmpAttempt(ctx, env, icmpExternalDst, "icmp", icmpCount)
	recordICMPSamples(r, rtts)
	r.Metrics = icmpMetrics(icmpExternalDst, icmpCount, rtts)
	switch {
	case err != nil:
		markFail(r, errnoOf(err), "外部 ICMP 探测失败: "+err.Error())
	case len(rtts) == 0:
		markFail(r, "ETIMEDOUT", "3 个 ICMP 包均未收到回显")
	}
	return outcome{res: r}
}
