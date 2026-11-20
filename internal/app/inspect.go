package app

import (
	"fmt"
	"os"

	"luanbird/osdiag/internal/pack"
)

func inspectCmd(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "用法: osdiag inspect <包.tar.gz>")
		return 5
	}
	path := args[0]
	m, n, err := pack.Inspect(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "校验失败: %v\n", err)
		return 1
	}
	fmt.Printf("archive: %s\n", path)
	fmt.Printf("run: %s profile=%s status=%s duration_s=%d\n", m.Run.ID, m.Run.Profile, m.Run.Status, m.Run.DurationS)
	fmt.Printf("host: %s kernel=%s arch=%s env=%s\n", m.Host.Hostname, m.Host.Kernel, m.Host.Arch, m.Host.Env)
	fmt.Printf("files: %d artifacts=%d errors=%d\n", n, len(m.Artifacts), len(m.Errors))
	fmt.Println("OK: 哈希与 manifest 一致")
	return 0
}
