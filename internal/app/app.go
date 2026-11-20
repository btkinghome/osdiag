// Package app 实现 osdiag 的命令行入口与 run 生命周期。
package app

import (
	"flag"
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
  inspect   校验证据包完整性并打印摘要
  version   打印版本
  help      显示本帮助

run 参数：
  --profile quick|standard|deep   预设周期（默认 standard）
  --duration N                    覆盖诊断周期秒数
  --workdir DIR                   工作目录（默认 /var/lib/osdiag）
  --out DIR                       输出目录（默认 <workdir>/out）
  --allow-heavy                   允许 P1 主动探测（M0 仅记录，不执行）
  --allow-external                允许 P2 外网探测（M0 仅记录，不执行）
  --no-probe                      禁用全部主动探测

示例：
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
	case "inspect":
		return inspectCmd(args[1:])
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

func newRunFlags() (*flag.FlagSet, *runOptions) {
	o := &runOptions{}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&o.profile, "profile", "standard", "quick|standard|deep")
	fs.IntVar(&o.duration, "duration", 0, "诊断周期秒数（0 表示用 profile 默认值）")
	fs.StringVar(&o.workdir, "workdir", "/var/lib/osdiag", "工作目录")
	fs.StringVar(&o.out, "out", "", "输出目录（默认 <workdir>/out）")
	fs.BoolVar(&o.allowHeavy, "allow-heavy", false, "允许 P1 主动探测")
	fs.BoolVar(&o.allowExternal, "allow-external", false, "允许 P2 外网探测")
	fs.BoolVar(&o.noProbe, "no-probe", false, "禁用全部主动探测")
	return fs, o
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
