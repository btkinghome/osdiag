package probes

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// icmpPayloadMarker 用于过滤不属于本次探测的回显。
const icmpPayloadMarker = "osdiag-probe"

// icmpChecksum 计算 RFC 1071 反码和（调用方把校验和字段置 0）。
func icmpChecksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// buildICMPEcho 构造 ICMPv4 echo request。
func buildICMPEcho(id, seq uint16, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	b[0] = 8 // echo request
	b[1] = 0
	binary.BigEndian.PutUint16(b[4:6], id)
	binary.BigEndian.PutUint16(b[6:8], seq)
	copy(b[8:], payload)
	binary.BigEndian.PutUint16(b[2:4], icmpChecksum(b))
	return b
}

// parseICMPEchoReply 解析可能带 IPv4 头的 ICMP echo reply，校验 id/seq 与校验和。
func parseICMPEchoReply(raw []byte, id, seq uint16) ([]byte, bool) {
	if len(raw) >= 20 && raw[0]>>4 == 4 {
		ihl := int(raw[0]&0x0f) * 4
		if ihl < 20 || len(raw) < ihl {
			return nil, false
		}
		raw = raw[ihl:]
	}
	if len(raw) < 8 || raw[0] != 0 || raw[1] != 0 {
		return nil, false
	}
	if icmpChecksum(raw) != 0 {
		return nil, false
	}
	if binary.BigEndian.Uint16(raw[4:6]) != id || binary.BigEndian.Uint16(raw[6:8]) != seq {
		return nil, false
	}
	return raw[8:], true
}

// summarizeRTT 返回毫秒统计与逗号分隔的 RTT 列表。
func summarizeRTT(rtts []time.Duration) (minMs, avgMs, maxMs float64, list string) {
	if len(rtts) == 0 {
		return 0, 0, 0, ""
	}
	minMs = float64(rtts[0].Microseconds()) / 1000
	maxMs = minMs
	total := 0.0
	parts := make([]string, 0, len(rtts))
	for _, d := range rtts {
		ms := float64(d.Microseconds()) / 1000
		if ms < minMs {
			minMs = ms
		}
		if ms > maxMs {
			maxMs = ms
		}
		total += ms
		parts = append(parts, fmt.Sprintf("%.3f", ms))
	}
	return minMs, total / float64(len(rtts)), maxMs, strings.Join(parts, ",")
}
