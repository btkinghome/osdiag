//go:build !linux

package probes

// readDefaultGateway 在非 Linux 构建上不可用。
func readDefaultGateway() (gateway, iface string, err error) {
	return "", "", newErrnoError("ENOENT", "当前平台没有 /proc/net/route")
}
