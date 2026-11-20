//go:build linux

package probes

import "os"

// readDefaultGateway 读取 /proc/net/route。
func readDefaultGateway() (gateway, iface string, err error) {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	return parseDefaultGateway(f)
}
