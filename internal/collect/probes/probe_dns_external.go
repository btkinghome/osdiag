package probes

import (
	"context"
	"net"
	"strings"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
)

var dnsExternalServers = []string{"223.5.5.5", "8.8.8.8", "1.1.1.1"}

const (
	dnsQueryName        = "example.com"
	dnsPerTargetTimeout = 2 * time.Second
)

func durationMSPtr(d time.Duration) *float64 {
	ms := float64(d.Microseconds()) / 1000
	return &ms
}

// queryDNSA 向单个服务器发一个手写 DNS A 查询并解析响应。
func queryDNSA(ctx context.Context, server, name string, id uint16) (dnsAnswer, time.Duration, error) {
	var ans dnsAnswer
	addr := net.JoinHostPort(server, "53")
	dialer := &net.Dialer{}
	t0 := time.Now()
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		return ans, time.Since(t0), err
	}
	defer conn.Close()

	deadline := t0.Add(dnsPerTargetTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return ans, time.Since(t0), err
	}
	query, err := buildDNSQuery(name, dnsTypeA, id)
	if err != nil {
		return ans, time.Since(t0), err
	}
	if _, err := conn.Write(query); err != nil {
		return ans, time.Since(t0), err
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	dur := time.Since(t0)
	if err != nil {
		return ans, dur, err
	}
	ans, err = parseDNSResponse(buf[:n], id)
	if err != nil {
		return ans, dur, err
	}
	switch {
	case ans.Truncated:
		return ans, dur, newErrnoError("ETRUNC", "DNS 响应被截断")
	case ans.RCode != 0:
		return ans, dur, dnsRCodeError(ans.RCode)
	case len(ans.Answers) == 0:
		return ans, dur, newErrnoError("ENODATA", "DNS 响应没有 A 记录")
	}
	return ans, dur, nil
}

// probeDNSExternal 依次向三个公共 DNS 发 A 查询，逐目标记录结果。
func probeDNSExternal(ctx context.Context, env *collect.Env) outcome {
	r := newResult()
	id := uint16(time.Now().UnixNano())
	responded := 0
	var failures, answers []string
	var firstErr error
	firstServer := ""

	for i, server := range dnsExternalServers {
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		// 三个目标共享单项探测的 5s 上限：每个目标不超过 2s，
		// 且不超过剩余预算按剩余目标数均分，避免最后一个目标把整项探测拖超时。
		budget := dnsPerTargetTimeout
		if dl, ok := ctx.Deadline(); ok {
			if share := time.Until(dl) / time.Duration(len(dnsExternalServers)-i); share < budget {
				budget = share
			}
		}
		tctx, cancel := context.WithTimeout(ctx, budget)
		ans, dur, err := queryDNSA(tctx, server, dnsQueryName, id)
		cancel()
		var rttPtr *float64
		if err == nil {
			rttPtr = durationMSPtr(dur)
		}
		env.AddNetTarget(model.NetTarget{
			Target:    server,
			Proto:     "dns",
			Attempted: true,
			OK:        err == nil,
			RTTMs:     rttPtr,
		})
		r.Samples = append(r.Samples, sample(time.Now(), "dns_a_query:"+server, dur, int64(len(ans.Answers)), errnoOf(err)))
		if err != nil {
			failures = append(failures, server+"="+errnoOf(err))
			if firstErr == nil {
				firstErr, firstServer = err, server
			}
			continue
		}
		responded++
		answers = append(answers, server+"="+strings.Join(ans.Answers, "|"))
	}

	r.Metrics = map[string]any{
		"name":           dnsQueryName,
		"qtype":          "A",
		"servers":        len(dnsExternalServers),
		"responded":      responded,
		"failures":       len(failures),
		"failed_targets": strings.Join(failures, ","),
		"answers":        strings.Join(answers, ","),
		"timeout_ms":     int(dnsPerTargetTimeout / time.Millisecond),
		"ok_condition":   "responded>=1",
	}
	if responded == 0 {
		if firstErr == nil {
			firstErr = newErrnoError("ECANCELED", "未执行任何 DNS 查询")
		}
		markFail(r, errnoOf(firstErr), "全部 DNS 目标失败（最后一个 "+firstServer+"）: "+firstErr.Error())
	}
	return outcome{res: r}
}
