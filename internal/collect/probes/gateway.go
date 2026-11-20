package probes

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

// parseDefaultGateway 从 /proc/net/route 文本中挑出 metric 最小的默认网关。
// 返回网关 IPv4 地址与出口网卡名。
func parseDefaultGateway(r io.Reader) (gateway, iface string, err error) {
	sc := bufio.NewScanner(r)
	bestMetric := -1
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 8 {
			continue
		}
		if fields[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 32)
		if err != nil {
			continue
		}
		const rtfGateway = 0x2
		if flags&rtfGateway == 0 {
			continue
		}
		metric, err := strconv.ParseUint(fields[6], 10, 32)
		if err != nil {
			continue
		}
		if bestMetric >= 0 && int(metric) >= bestMetric {
			continue
		}
		ip, err := hexLEToIPv4(fields[2])
		if err != nil {
			continue
		}
		gateway, iface, bestMetric = ip, fields[0], int(metric)
	}
	if err := sc.Err(); err != nil {
		return "", "", err
	}
	if gateway == "" {
		return "", "", newErrnoError("ENOENT", "/proc/net/route 中没有默认网关")
	}
	return gateway, iface, nil
}

// hexLEToIPv4 把 /proc/net/route 的小端 8 位十六进制地址转成点分 IPv4。
func hexLEToIPv4(s string) (string, error) {
	if len(s) != 8 {
		return "", fmt.Errorf("非法地址字段 %q", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return "", err
	}
	ip := net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
	return ip.String(), nil
}
