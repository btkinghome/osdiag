// Package app 实现 osdiag 的命令行入口与 run 生命周期。
package app

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"luanbird/osdiag/internal/version"
)

const usageText = `osdiag —— Linux 主机诊断证据采集工具（只采集，不研判）

用法：
  osdiag <command> [flags]

命令：
  run       执行一次诊断周期，生成 tar.gz 证据包
  preflight 检查 root/procfs/内核/架构/工作目录/磁盘，不采集
  inspect   校验证据包完整性并打印摘要
  list      列出工作目录下已有的证据包
  version   打印版本
  help      显示本帮助

run 参数：
  --profile quick|standard|deep   预设周期（默认 standard）
  --duration N                    覆盖诊断周期秒数
  --workdir DIR                   工作目录（默认 /var/lib/osdiag）
  --out DIR                       输出目录（默认 <workdir>/out）
  --allow-heavy                   允许 P1 主动探测（沙箱写、短时 CPU/内存压力、ICMP）
  --allow-external                允许 P2 外网探测
  --no-probe                      禁用全部主动探测
  --journal-file FILE             导入 journalctl -o export 文本（逗号分隔可多个）
  --with-environ                  采集进程环境变量（默认不采集）

preflight 参数：
  --workdir DIR                   工作目录（默认 /var/lib/osdiag）

list 参数：
  --workdir DIR                   工作目录（默认 /var/lib/osdiag）
  --out DIR                       输出目录（默认 <workdir>/out）

示例：
  osdiag preflight --workdir /var/lib/osdiag
  osdiag list
  osdiag run --profile quick
  osdiag inspect /var/lib/osdiag/out/osdiag_host_20261120T070000Z-a1b2c3.tar.gz
`

// Main 是进程入口，返回退出码。
func Main(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		return 5
	}
	switch args[0] {
	case "run":
		return runCmd(args[1:])
	case "preflight":
		return preflightCmd(args[1:])
	case "inspect":
		return inspectCmd(args[1:])
	case "list":
		return listCmd(args[1:])
	case "version":
		fmt.Printf("osdiag %s (commit %s, build %s, %s)\n", version.Version, version.Commit, version.Build, runtime.Version())
		return 0
	case "help", "-h", "--help":
		fmt.Print(usageText)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n%s", args[0], usageText)
		return 5
	}
}

func sanitizeHost(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}
