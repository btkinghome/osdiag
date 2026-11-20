package probes

import (
	"bytes"
	"context"
	"io"
	"net"
	"time"

	"luanbird/osdiag/internal/collect"
)

const loopbackPayload = 32

// probeNetLoopback 在本机 127.0.0.1:0 上完成 listen → dial → 32B echo，
// 用回显往返时间作为 RTT。
func probeNetLoopback(ctx context.Context, _ *collect.Env) outcome {
	r := newResult()
	if err := ctx.Err(); err != nil {
		markFail(r, errnoOf(err), "net.loopback 开始前已取消: "+err.Error())
		return outcome{res: r}
	}

	t0 := time.Now()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	listenDur := time.Since(t0)
	r.Samples = append(r.Samples, sample(t0, "listen", listenDur, -1, errnoOf(err)))
	if err != nil {
		markFail(r, errnoOf(err), "本地监听失败: "+err.Error())
		return outcome{res: r}
	}
	defer ln.Close()
	addr := ln.Addr().String()

	srvErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			srvErr <- err
			return
		}
		defer conn.Close()
		buf := make([]byte, loopbackPayload)
		if _, err := io.ReadFull(conn, buf); err != nil {
			srvErr <- err
			return
		}
		_, err = conn.Write(buf)
		srvErr <- err
	}()

	t1 := time.Now()
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	dialDur := time.Since(t1)
	r.Samples = append(r.Samples, sample(t1, "dial", dialDur, -1, errnoOf(err)))
	if err != nil {
		markFail(r, errnoOf(err), "连接本地监听失败: "+err.Error())
		return outcome{res: r}
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	payload := bytes.Repeat([]byte{'o'}, loopbackPayload)
	t2 := time.Now()
	if _, err := conn.Write(payload); err != nil {
		markFail(r, errnoOf(err), "写入回显数据失败: "+err.Error())
		return outcome{res: r}
	}
	got := make([]byte, loopbackPayload)
	if _, err := io.ReadFull(conn, got); err != nil {
		markFail(r, errnoOf(err), "读取回显数据失败: "+err.Error())
		return outcome{res: r}
	}
	rtt := time.Since(t2)
	r.Samples = append(r.Samples, sample(t2, "echo", rtt, int64(len(got)), ""))
	if !bytes.Equal(got, payload) {
		markFail(r, "EIO", "回显内容与发送内容不一致")
		return outcome{res: r}
	}

	select {
	case err := <-srvErr:
		if err != nil {
			markFail(r, errnoOf(err), "服务端回显失败: "+err.Error())
			return outcome{res: r}
		}
	case <-ctx.Done():
		markFail(r, errnoOf(ctx.Err()), "等待服务端回显时被中断: "+ctx.Err().Error())
		return outcome{res: r}
	}

	r.Metrics = map[string]any{
		"listen_addr": addr,
		"bytes":       len(got),
		"rtt_us":      round3(float64(rtt.Microseconds())),
		"listen_us":   round3(float64(listenDur.Microseconds())),
		"dial_us":     round3(float64(dialDur.Microseconds())),
	}
	return outcome{res: r}
}
