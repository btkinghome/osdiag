package app

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"luanbird/osdiag/internal/sysinfo"
)

// preflightCmd 只做环境检查，不采集、不写证据包。退出码 3 表示不满足运行条件。
func preflightCmd(args []string) int {
	fs := flag.NewFlagSet("preflight", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	workdir := fs.String("workdir", "/var/lib/osdiag", "工作目录")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法: osdiag preflight [--workdir DIR]")
	}
	if err := fs.Parse(args); err != nil {
		return 5
	}

	fails := 0
	report := func(ok bool, name, detail string) {
		mark := "OK  "
		if !ok {
			mark = "FAIL"
			fails++
		}
		fmt.Printf("%s  %-14s %s\n", mark, name, detail)
	}

	uid := os.Getuid()
	report(uid == 0, "root", fmt.Sprintf("uid=%d（需要 root 读取 kmsg/SMART/部分 /proc）", uid))

	if _, err := os.Stat("/proc/self/status"); err != nil {
		report(false, "procfs", err.Error())
	} else {
		report(true, "procfs", "/proc/self/status 可读")
	}

	release := sysinfo.KernelRelease()
	report(sysinfo.KernelBaselineOK(release), "kernel", fmt.Sprintf("%s（要求 >= 4.19）", release))
	report(runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64", "arch", runtime.GOARCH)

	if err := os.MkdirAll(*workdir, 0o755); err != nil {
		report(false, "workdir", err.Error())
	} else {
		probe := filepath.Join(*workdir, ".preflight-write-test")
		if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
			report(false, "workdir", fmt.Sprintf("%s 不可写: %v", *workdir, err))
		} else {
			_ = os.Remove(probe)
			report(true, "workdir", *workdir+" 可写")
		}
		if free, err := freeBytes(*workdir); err == nil {
			report(free >= 512<<20, "disk", fmt.Sprintf("%s 可用 %d MiB（建议 >= 512 MiB）", *workdir, free>>20))
		} else {
			report(false, "disk", err.Error())
		}
	}

	caps := sysinfo.Capabilities()
	present := 0
	for _, c := range caps {
		if c.Present && c.Readable {
			present++
		}
	}
	fmt.Printf("INFO  %-14s %d/%d 项能力可用（详见 run 后 manifest.capabilities）\n", "capabilities", present, len(caps))

	if fails > 0 {
		fmt.Fprintf(os.Stderr, "preflight 失败：%d 项不满足，修正后再运行 osdiag run\n", fails)
		return 3
	}
	fmt.Println("preflight 通过")
	return 0
}
