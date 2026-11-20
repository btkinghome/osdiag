package probes

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
)

const (
	dnsTypeA     = 1
	dnsTypeCNAME = 5
	dnsClassIN   = 1
)

// dnsAnswer 是解析后的 DNS 响应摘要。
type dnsAnswer struct {
	ID        uint16
	RCode     int
	Answers   []string
	Truncated bool
}

// buildDNSQuery 手工构造一个递归查询报文（QDCOUNT=1）。
func buildDNSQuery(name string, qtype uint16, id uint16) ([]byte, error) {
	qname, err := encodeDNSName(name)
	if err != nil {
		return nil, err
	}
	msg := make([]byte, 12, 12+len(qname)+4)
	binary.BigEndian.PutUint16(msg[0:2], id)
	binary.BigEndian.PutUint16(msg[2:4], 0x0100) // RD=1
	binary.BigEndian.PutUint16(msg[4:6], 1)      // QDCOUNT
	msg = append(msg, qname...)
	var tail [4]byte
	binary.BigEndian.PutUint16(tail[0:2], qtype)
	binary.BigEndian.PutUint16(tail[2:4], dnsClassIN)
	msg = append(msg, tail[:]...)
	return msg, nil
}

// encodeDNSName 把点分域名编码为 DNS 标签序列（不含压缩）。
func encodeDNSName(name string) ([]byte, error) {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	if name == "" {
		return []byte{0}, nil
	}
	var out []byte
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			return nil, newErrnoError("EINVAL", "域名包含空标签: "+name)
		}
		if len(label) > 63 {
			return nil, newErrnoError("EINVAL", "域名标签超过 63 字节")
		}
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	if len(out)+1 > 255 {
		return nil, newErrnoError("EINVAL", "域名编码超过 255 字节")
	}
	out = append(out, 0)
	return out, nil
}

// parseDNSResponse 解析响应头与回答段的 A/CNAME 记录，支持压缩指针。
func parseDNSResponse(msg []byte, wantID uint16) (dnsAnswer, error) {
	var a dnsAnswer
	if len(msg) < 12 {
		return a, newErrnoError("EDNS", "DNS 响应短于 12 字节")
	}
	a.ID = binary.BigEndian.Uint16(msg[0:2])
	flags := binary.BigEndian.Uint16(msg[2:4])
	if a.ID != wantID {
		return a, newErrnoError("EDNSID", "DNS 事务 ID 不匹配")
	}
	if flags&0x8000 == 0 {
		return a, newErrnoError("EDNS", "DNS 响应缺少 QR 位")
	}
	a.RCode = int(flags & 0x000f)
	a.Truncated = flags&0x0200 != 0
	qd := int(binary.BigEndian.Uint16(msg[4:6]))
	an := int(binary.BigEndian.Uint16(msg[6:8]))

	off := 12
	for i := 0; i < qd; i++ {
		var err error
		off, err = skipDNSName(msg, off)
		if err != nil {
			return a, err
		}
		if off+4 > len(msg) {
			return a, newErrnoError("EDNS", "问题段被截断")
		}
		off += 4
	}

	seen := map[string]bool{}
	for i := 0; i < an; i++ {
		var err error
		off, err = skipDNSName(msg, off)
		if err != nil {
			return a, err
		}
		if off+10 > len(msg) {
			return a, newErrnoError("EDNS", "回答段被截断")
		}
		typ := binary.BigEndian.Uint16(msg[off : off+2])
		class := binary.BigEndian.Uint16(msg[off+2 : off+4])
		rdlen := int(binary.BigEndian.Uint16(msg[off+8 : off+10]))
		off += 10
		if off+rdlen > len(msg) {
			return a, newErrnoError("EDNS", "RDATA 被截断")
		}
		if typ == dnsTypeA && class == dnsClassIN && rdlen == 4 {
			ip := net.IP(msg[off : off+4]).String()
			if !seen[ip] {
				seen[ip] = true
				a.Answers = append(a.Answers, ip)
			}
		}
		off += rdlen
	}
	return a, nil
}

// skipDNSName 跳过可能带压缩指针的域名，返回其后的偏移。
func skipDNSName(msg []byte, off int) (int, error) {
	for {
		if off >= len(msg) {
			return 0, newErrnoError("EDNS", "域名偏移越界")
		}
		l := int(msg[off])
		switch {
		case l == 0:
			return off + 1, nil
		case l&0xc0 == 0xc0:
			if off+2 > len(msg) {
				return 0, newErrnoError("EDNS", "压缩指针越界")
			}
			return off + 2, nil
		case l&0xc0 != 0:
			return 0, newErrnoError("EDNS", "非法标签长度")
		default:
			off += 1 + l
		}
	}
}

// dnsRCodeError 把 RCODE 映射为可读 errno。
func dnsRCodeError(rcode int) error {
	switch rcode {
	case 1:
		return newErrnoError("EDNS_FORMERR", "DNS 服务器返回 FORMERR")
	case 2:
		return newErrnoError("EDNS_SERVFAIL", "DNS 服务器返回 SERVFAIL")
	case 3:
		return newErrnoError("ENOTFOUND", "DNS 服务器返回 NXDOMAIN")
	case 4:
		return newErrnoError("EDNS_NOTIMP", "DNS 服务器返回 NOTIMP")
	case 5:
		return newErrnoError("EDNS_REFUSED", "DNS 服务器返回 REFUSED")
	default:
		return newErrnoError("EDNS_RCODE", fmt.Sprintf("DNS 服务器返回 RCODE=%d", rcode))
	}
}
