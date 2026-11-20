package probes

import (
	"context"
	"net"
	"strings"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
)

var tcpExternalTargets = []string{"8.8.8.8:443", "1.1.1.1:443"}

const tcpPerTargetTimeout = 2 * time.Second

// probeTCPExternal 对两个外部 443 端口做 TCP connect 并记录 RTT。
func probeTCPExternal(ctx context.Context, env *collect.Env) outcome {
	r := newResult()
	connected := 0
	var failures []string
	var rtts []time.Duration
	var firstErr error
	firstTarget := ""

	for _, target := range tcpExternalTargets {
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		tctx, cancel := context.WithTimeout(ctx, tcpPerTargetTimeout)
		t0 := time.Now()
		conn, err := (&net.Dialer{}).DialContext(tctx, "tcp", target)
		dur := time.Since(t0)
		cancel()
		if conn != nil {
			_ = conn.Close()
		}
		var rttPtr *float64
		if err == nil {
			rttPtr = durationMSPtr(dur)
		}
		env.AddNetTarget(model.NetTarget{
			Target:    target,
			Proto:     "tcp",
			Attempted: true,
			OK:        err == nil,
			RTTMs:     rttPtr,
		})
		r.Samples = append(r.Samples, sample(t0, "tcp_connect", dur, -1, errnoOf(err)))
		if err != nil {
			failures = append(failures, target+"="+errnoOf(err))
			if firstErr == nil {
				firstErr, firstTarget = err, target
			}
			continue
		}
		connected++
		rtts = append(rtts, dur)
	}

	minMs, avgMs, maxMs, list := summarizeRTT(rtts)
	r.Metrics = map[string]any{
		"targets":        len(tcpExternalTargets),
		"connected":      connected,
		"failures":       len(failures),
		"failed_targets": strings.Join(failures, ","),
		"min_rtt_ms":     round3(minMs),
		"avg_rtt_ms":     round3(avgMs),
		"max_rtt_ms":     round3(maxMs),
		"rtts_ms":        list,
		"timeout_ms":     int(tcpPerTargetTimeout / time.Millisecond),
		"ok_condition":   "connected>=1",
	}
	if connected == 0 {
		if firstErr == nil {
			firstErr = newErrnoError("ECANCELED", "未执行任何 TCP 连接")
		}
		markFail(r, errnoOf(firstErr), "全部 TCP 目标失败（最后一个 "+firstTarget+"）: "+firstErr.Error())
	}
	return outcome{res: r}
}
