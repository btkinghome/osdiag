package proc

import (
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"luanbird/osdiag/internal/model"
)

// tcpStateName 把 /proc/net/tcp{,6} 的 st 十六进制码转为常见状态名。
func tcpStateName(code string) string {
	switch strings.ToUpper(code) {
	case "01":
		return "ESTABLISHED"
	case "02":
		return "SYN_SENT"
	case "03":
		return "SYN_RECV"
	case "04":
		return "FIN_WAIT1"
	case "05":
		return "FIN_WAIT2"
	case "06":
		return "TIME_WAIT"
	case "07":
		return "CLOSE"
	case "08":
		return "CLOSE_WAIT"
	case "09":
		return "LAST_ACK"
	case "0A":
		return "LISTEN"
	case "0B":
		return "CLOSING"
	case "0C":
		return "NEW_SYN_RECV"
	default:
		return strings.ToUpper(code)
	}
}

// tcpSocketMap 解析 /proc/net/tcp 与 tcp6，返回 inode -> socket 引用。
// 用于把 /proc/<pid>/fd 的 socket inode 反查成端口；解析失败的文件忽略。
func tcpSocketMap() map[int64]model.SocketRef {
	type tcpFile struct {
		name  string
		proto string
		v6    bool
	}
	files := []tcpFile{
		{"net/tcp", "tcp", false},
		{"net/tcp6", "tcp6", true},
	}
	out := make(map[int64]model.SocketRef)
	for _, tf := range files {
		raw, err := os.ReadFile(filepath.Join(procPath, tf.name))
		if err != nil {
			continue
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			if i == 0 || strings.TrimSpace(line) == "" {
				continue // 首行是表头
			}
			fields := strings.Fields(line)
			if len(fields) < 10 {
				continue
			}
			inode, err := strconv.ParseInt(fields[9], 10, 64)
			if err != nil || inode <= 0 {
				continue // TIME_WAIT 等无 inode 的行
			}
			ref := model.SocketRef{
				Proto:  tf.proto,
				Local:  formatEndpoint(fields[1], tf.v6),
				Remote: formatEndpoint(fields[2], tf.v6),
				State:  tcpStateName(fields[3]),
				Inode:  int64Ptr(inode),
			}
			out[inode] = ref
		}
	}
	return out
}

// countListening 统计 /proc/net/tcp{,6} 中 state=0A 的行数。
// 两个文件都读不到时才返回错误，部分可读按 best-effort 计数。
func countListening() (int, error) {
	total := 0
	readable := 0
	var firstErr error
	for _, name := range []string{"net/tcp", "net/tcp6"} {
		raw, err := os.ReadFile(filepath.Join(procPath, name))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		readable++
		for i, line := range strings.Split(string(raw), "\n") {
			if i == 0 {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 4 && strings.EqualFold(fields[3], "0A") {
				total++
			}
		}
	}
	if readable == 0 {
		return 0, firstErr
	}
	return total, nil
}

// scanProcSockets 反查 /proc/<pid>/fd 的 socket inode；budget 为本次快照的
// readlink 总预算，耗尽后剩余进程不再做映射。
func scanProcSockets(pid int, sockets map[int64]model.SocketRef, budget *int) []model.SocketRef {
	if len(sockets) == 0 || budget == nil || *budget <= 0 {
		return nil
	}
	dir := procFile(pid, "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	seen := make(map[int64]bool, 4)
	var out []model.SocketRef
	for _, ent := range entries {
		if *budget <= 0 {
			break
		}
		*budget--
		link, err := os.Readlink(filepath.Join(dir, ent.Name()))
		if err != nil {
			continue
		}
		inode, ok := parseSocketLink(link)
		if !ok || seen[inode] {
			continue
		}
		ref, ok := sockets[inode]
		if !ok {
			continue
		}
		seen[inode] = true
		out = append(out, ref)
		if len(out) >= maxSocketsPerProc {
			break
		}
	}
	return out
}

// parseSocketLink 解析 "socket:[12345]"。
func parseSocketLink(link string) (int64, bool) {
	const prefix = "socket:["
	if !strings.HasPrefix(link, prefix) || !strings.HasSuffix(link, "]") {
		return 0, false
	}
	inode, err := strconv.ParseInt(link[len(prefix):len(link)-1], 10, 64)
	if err != nil || inode <= 0 {
		return 0, false
	}
	return inode, true
}

// formatEndpoint 把 "0100007F:1F90" 转成 "127.0.0.1:8080"。
// /proc/net/tcp 的地址是主机字节序（小端）的十六进制。
func formatEndpoint(addrPort string, v6 bool) string {
	addrHex, portHex, ok := strings.Cut(addrPort, ":")
	if !ok {
		return addrPort
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return addrPort
	}
	host := formatAddr(addrHex, v6)
	if host == "" {
		return addrPort
	}
	return net.JoinHostPort(host, strconv.FormatUint(port, 10))
}

func formatAddr(addrHex string, v6 bool) string {
	if !v6 {
		v, err := strconv.ParseUint(addrHex, 16, 32)
		if err != nil || len(addrHex) != 8 {
			return ""
		}
		return net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24)).String()
	}
	if len(addrHex) != 32 {
		return ""
	}
	var ip [16]byte
	for i := 0; i < 4; i++ {
		v, err := strconv.ParseUint(addrHex[i*8:(i+1)*8], 16, 32)
		if err != nil {
			return ""
		}
		binary.LittleEndian.PutUint32(ip[i*4:], uint32(v))
	}
	return net.IP(ip[:]).String()
}

func int64Ptr(v int64) *int64 { return &v }
