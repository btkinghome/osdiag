//go:build linux

package probes

import (
	"context"
	"net"
	"os"
	"strings"
	"syscall"
	"time"
)

// icmpPing 用原始 socket 发最多 count 个 ICMPv4 echo，需要 root 或 CAP_NET_RAW。
func icmpPing(ctx context.Context, dst net.IP, count int, perPacket time.Duration) ([]time.Duration, error) {
	ip4 := dst.To4()
	if ip4 == nil {
		return nil, newErrnoError("EINVAL", "仅支持 IPv4 ICMP")
	}
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_ICMP)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)

	timeout := perPacket
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem < timeout {
			timeout = rem
		}
	}
	if timeout <= 0 {
		return nil, context.DeadlineExceeded
	}
	tv := syscall.NsecToTimeval(timeout.Nanoseconds())
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		return nil, err
	}

	id := uint16(os.Getpid())
	rtts := make([]time.Duration, 0, count)
	var timeoutErr error
	for seq := 0; seq < count; seq++ {
		if err := ctx.Err(); err != nil {
			return rtts, err
		}
		pkt := buildICMPEcho(id, uint16(seq), []byte(icmpPayloadMarker))
		sa := &syscall.SockaddrInet4{}
		copy(sa.Addr[:], ip4)
		t0 := time.Now()
		if err := syscall.Sendto(fd, pkt, 0, sa); err != nil {
			return rtts, err
		}
		buf := make([]byte, 1500)
		for {
			n, _, err := syscall.Recvfrom(fd, buf, 0)
			if err != nil {
				if err == syscall.EINTR {
					continue
				}
				if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
					timeoutErr = newErrnoError("ETIMEDOUT", "ICMP 回显等待超时")
					break
				}
				return rtts, err
			}
			payload, ok := parseICMPEchoReply(buf[:n], id, uint16(seq))
			if ok && strings.HasPrefix(string(payload), icmpPayloadMarker) {
				rtts = append(rtts, time.Since(t0))
				break
			}
		}
	}
	if len(rtts) == 0 && timeoutErr != nil {
		return nil, timeoutErr
	}
	return rtts, nil
}
