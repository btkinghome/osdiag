package probes

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestICMPChecksumAndBuild(t *testing.T) {
	pkt := buildICMPEcho(0x1234, 7, []byte("osdiag-probe"))
	if pkt[0] != 8 || pkt[1] != 0 {
		t.Fatalf("type/code = %d/%d, want 8/0", pkt[0], pkt[1])
	}
	if got := binary.BigEndian.Uint16(pkt[4:6]); got != 0x1234 {
		t.Errorf("id = %#x, want 0x1234", got)
	}
	if got := binary.BigEndian.Uint16(pkt[6:8]); got != 7 {
		t.Errorf("seq = %d, want 7", got)
	}
	if !bytes.Equal(pkt[8:], []byte("osdiag-probe")) {
		t.Errorf("payload = %q", pkt[8:])
	}
	if sum := icmpChecksum(pkt); sum != 0 {
		t.Errorf("含校验和的报文反码和 = %#x, want 0", sum)
	}

	cases := [][]byte{
		{},
		{0x08},
		{0x08, 0x00, 0x00},
		{0x08, 0x00, 0x00, 0x00, 0x12},
	}
	for _, b := range cases {
		if _, ok := parseICMPEchoReply(b, 0x1234, 7); ok {
			t.Errorf("parseICMPEchoReply(% x) 不应成功", b)
		}
	}
}

func TestParseICMPEchoReply(t *testing.T) {
	payload := []byte("osdiag-probe")
	req := buildICMPEcho(0xbeef, 3, payload)
	reply := append([]byte{}, req...)
	reply[0] = 0 // echo reply
	binary.BigEndian.PutUint16(reply[2:4], 0)
	binary.BigEndian.PutUint16(reply[2:4], icmpChecksum(reply))

	// 带 20 字节 IPv4 头的形态（Linux raw socket 的接收格式）。
	ipHeader := make([]byte, 20)
	ipHeader[0] = 0x45
	withIP := append(ipHeader, reply...)

	cases := []struct {
		name    string
		raw     []byte
		id      uint16
		seq     uint16
		wantOK  bool
		wantPay []byte
	}{
		{"纯 ICMP 回显", reply, 0xbeef, 3, true, payload},
		{"带 IPv4 头", withIP, 0xbeef, 3, true, payload},
		{"id 不匹配", reply, 0x0000, 3, false, nil},
		{"seq 不匹配", reply, 0xbeef, 4, false, nil},
		{"校验和错误", corruptChecksum(reply), 0xbeef, 3, false, nil},
		{"IPv4 头不完整", withIP[:10], 0xbeef, 3, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseICMPEchoReply(tc.raw, tc.id, tc.seq)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && !bytes.Equal(got, tc.wantPay) {
				t.Errorf("payload = %q, want %q", got, tc.wantPay)
			}
		})
	}
}

func corruptChecksum(b []byte) []byte {
	out := append([]byte{}, b...)
	out[2] ^= 0xff
	return out
}

func TestSummarizeRTT(t *testing.T) {
	minMs, avgMs, maxMs, list := summarizeRTT(nil)
	if minMs != 0 || avgMs != 0 || maxMs != 0 || list != "" {
		t.Errorf("空列表得到 (%v,%v,%v,%q)", minMs, avgMs, maxMs, list)
	}
}
