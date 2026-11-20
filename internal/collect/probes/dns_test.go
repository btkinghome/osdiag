package probes

import (
	"bytes"
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

func testARecord(ip string) []byte {
	b := []byte{0xc0, 0x0c, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x3c, 0x00, 0x04}
	return append(b, net.ParseIP(ip).To4()...)
}

func testCNAMERecord(target string) []byte {
	rdata := []byte{byte(len(target))}
	rdata = append(rdata, target...)
	rdata = append(rdata, 0xc0, 0x0c)
	b := []byte{0xc0, 0x0c, 0x00, 0x05, 0x00, 0x01, 0x00, 0x00, 0x00, 0x3c, 0x00, byte(len(rdata))}
	return append(b, rdata...)
}

func testDNSResponse(id uint16, flags uint16, answers [][]byte) []byte {
	qname, _ := encodeDNSName("example.com")
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[0:2], id)
	binary.BigEndian.PutUint16(b[2:4], flags)
	binary.BigEndian.PutUint16(b[4:6], 1)
	binary.BigEndian.PutUint16(b[6:8], uint16(len(answers)))
	b = append(b, qname...)
	b = append(b, 0x00, 0x01, 0x00, 0x01)
	for _, rr := range answers {
		b = append(b, rr...)
	}
	return b
}

func TestBuildDNSQuery(t *testing.T) {
	q, err := buildDNSQuery("example.com", dnsTypeA, 0x1234)
	if err != nil {
		t.Fatalf("buildDNSQuery: %v", err)
	}
	if got := binary.BigEndian.Uint16(q[0:2]); got != 0x1234 {
		t.Errorf("id = %#x, want 0x1234", got)
	}
	if got := binary.BigEndian.Uint16(q[2:4]); got != 0x0100 {
		t.Errorf("flags = %#x, want RD", got)
	}
	if got := binary.BigEndian.Uint16(q[4:6]); got != 1 {
		t.Errorf("qdcount = %d, want 1", got)
	}
	if got := binary.BigEndian.Uint16(q[6:8]); got != 0 {
		t.Errorf("ancount = %d, want 0", got)
	}
	qname, _ := encodeDNSName("example.com")
	want := append([]byte{}, qname...)
	want = append(want, 0x00, 0x01, 0x00, 0x01)
	if !bytes.Equal(q[12:], want) {
		t.Errorf("question = % x, want % x", q[12:], want)
	}

	if _, err := buildDNSQuery("bad..name", dnsTypeA, 1); err == nil {
		t.Error("buildDNSQuery(bad..name) 应返回错误")
	}
}

func TestEncodeDNSName(t *testing.T) {
	longLabel := strings.Repeat("a", 64)
	longName := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." +
		strings.Repeat("c", 63) + "." + strings.Repeat("d", 63)
	cases := []struct {
		name    string
		want    []byte
		wantErr bool
	}{
		{"example.com", append(append([]byte{7}, "example"...), append([]byte{3}, "com\x00"...)...), false},
		{"example.com.", append(append([]byte{7}, "example"...), append([]byte{3}, "com\x00"...)...), false},
		{"localhost", append([]byte{9}, "localhost\x00"...), false},
		{"", []byte{0}, false},
		{"a..b", nil, true},
		{longLabel + ".com", nil, true},
		{longName, nil, true},
	}
	for _, tc := range cases {
		got, err := encodeDNSName(tc.name)
		if tc.wantErr {
			if err == nil {
				t.Errorf("encodeDNSName(%q) 期望错误", tc.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("encodeDNSName(%q): %v", tc.name, err)
			continue
		}
		if !bytes.Equal(got, tc.want) {
			t.Errorf("encodeDNSName(%q) = % x, want % x", tc.name, got, tc.want)
		}
	}
}

func TestParseDNSResponse(t *testing.T) {
	const id = 0xabcd
	const normalFlags = 0x8180 // QR|RD|RA
	a1 := testARecord("93.184.216.34")
	a2 := testARecord("1.2.3.4")
	a3 := testARecord("1.2.3.4") // 去重
	a4 := testARecord("5.6.7.8")

	rdataOverflow := []byte{0xc0, 0x0c, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x3c, 0x00, 0x04, 0x01, 0x02}
	aaaa := []byte{0xc0, 0x0c, 0x00, 0x1c, 0x00, 0x01, 0x00, 0x00, 0x00, 0x3c, 0x00, 0x04, 1, 2, 3, 4}

	cases := []struct {
		name      string
		msg       []byte
		wantID    uint16
		wantAns   []string
		wantRCode int
		wantTrunc bool
		wantErr   bool
	}{
		{"单个 A", testDNSResponse(id, normalFlags, [][]byte{a1}), id, []string{"93.184.216.34"}, 0, false, false},
		{"CNAME 后多个 A 且去重", testDNSResponse(id, normalFlags, [][]byte{testCNAMERecord("www"), a2, a3, a4}), id, []string{"1.2.3.4", "5.6.7.8"}, 0, false, false},
		{"NXDOMAIN", testDNSResponse(id, normalFlags|0x0003, nil), id, nil, 3, false, false},
		{"TC 置位", testDNSResponse(id, normalFlags|0x0200, nil), id, nil, 0, true, false},
		{"非 A 记录被忽略", testDNSResponse(id, normalFlags, [][]byte{aaaa}), id, nil, 0, false, false},
		{"事务 ID 不匹配", testDNSResponse(id, normalFlags, [][]byte{a1}), id + 1, nil, 0, false, true},
		{"缺少 QR 位", testDNSResponse(id, 0x0100, nil), id, nil, 0, false, true},
		{"RDATA 越界", testDNSResponse(id, normalFlags, [][]byte{rdataOverflow}), id, nil, 0, false, true},
		{"响应过短", []byte{0x00, 0x01}, id, nil, 0, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDNSResponse(tc.msg, tc.wantID)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望错误，实际得到 %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDNSResponse: %v", err)
			}
			if got.ID != id {
				t.Errorf("ID = %#x, want %#x", got.ID, id)
			}
			if got.RCode != tc.wantRCode {
				t.Errorf("RCode = %d, want %d", got.RCode, tc.wantRCode)
			}
			if got.Truncated != tc.wantTrunc {
				t.Errorf("Truncated = %v, want %v", got.Truncated, tc.wantTrunc)
			}
			if strings.Join(got.Answers, ",") != strings.Join(tc.wantAns, ",") {
				t.Errorf("Answers = %v, want %v", got.Answers, tc.wantAns)
			}
		})
	}
}

func TestDNSRCodeError(t *testing.T) {
	cases := []struct {
		rcode     int
		wantErrno string
	}{
		{1, "EDNS_FORMERR"},
		{2, "EDNS_SERVFAIL"},
		{3, "ENOTFOUND"},
		{4, "EDNS_NOTIMP"},
		{5, "EDNS_REFUSED"},
		{9, "EDNS_RCODE"},
	}
	for _, tc := range cases {
		err := dnsRCodeError(tc.rcode)
		if got := errnoOf(err); got != tc.wantErrno {
			t.Errorf("rcode %d → errno %q, want %q", tc.rcode, got, tc.wantErrno)
		}
	}
}
