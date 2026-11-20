package app

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// listCmd 列出工作目录 out/ 下已有的证据包，按修改时间倒序。
func listCmd(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	workdir := fs.String("workdir", "/var/lib/osdiag", "工作目录")
	out := fs.String("out", "", "输出目录（默认 <workdir>/out）")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法: osdiag list [--workdir DIR] [--out DIR]")
	}
	if err := fs.Parse(args); err != nil {
		return 5
	}
	dir := *out
	if dir == "" {
		dir = filepath.Join(*workdir, "out")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取 %s: %v\n", dir, err)
		return 1
	}
	type item struct {
		name string
		size int64
		mod  string
	}
	var items []item
	var total int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{e.Name(), info.Size(), info.ModTime().Format("2006-01-02T15:04:05Z07:00")})
		total += info.Size()
	}
	if len(items) == 0 {
		fmt.Printf("目录 %s 下没有证据包\n", dir)
		return 0
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod > items[j].mod })
	fmt.Printf("目录: %s\n\n", dir)
	fmt.Printf("%-20s %10s  %s\n", "RUN", "SIZE", "MODIFIED")
	for _, it := range items {
		fmt.Printf("%-20s %10s  %s\n", strings.TrimSuffix(it.name, ".tar.gz"), humanSize(it.size), it.mod)
	}
	fmt.Printf("\n共 %d 个包，合计 %s\n", len(items), humanSize(total))
	return 0
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
