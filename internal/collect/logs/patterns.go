package logs

import (
	"bufio"
	"context"
	"os"
	"regexp"
	"strings"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
)

// 类别名称与设计文档附录 A 对齐，并对样例包中更细的类别
// （nvme_timeout / link_flap / systemd_unit_failed）保留原名。
type patternDef struct {
	category string
	re       *regexp.Regexp
}

var patternDefs = []patternDef{
	{category: "oops_panic", re: regexp.MustCompile(`BUG:|kernel BUG at|Oops:|Kernel panic|general protection fault`)},
	{category: "call_trace", re: regexp.MustCompile(`Call Trace:|RIP:`)},
	{category: "lockup", re: regexp.MustCompile(`(?i)soft lockup|hard LOCKUP|RCU stall`)},
	{category: "hung_task", re: regexp.MustCompile(`(?i)hung_task|blocked for more than`)},
	{category: "mce", re: regexp.MustCompile(`Machine Check|MCE|Hardware Error`)},
	{category: "io_error", re: regexp.MustCompile(`I/O error|blk_update_request|Buffer I/O error|medium error`)},
	{category: "fs_error", re: regexp.MustCompile(`EXT4-fs error|XFS.*corruption|BTRFS error|remount.*read-only`)},
	{category: "oom", re: regexp.MustCompile(`Out of memory|oom-kill|Killed process`)},
	{category: "link_flap", re: regexp.MustCompile(`(?i)link is not ready|NIC Link is Down|link status (down|up)|link down|link up`)},
	{category: "net_link", re: regexp.MustCompile(`(?i)tx timeout|NETDEV WATCHDOG`)},
	{category: "net_tcp", re: regexp.MustCompile(`TCP: request_sock|possible SYN flooding|connection reset`)},
	{category: "scsi_ata", re: regexp.MustCompile(`ata[0-9]+.*error|Sense Key|medium error|reset failed`)},
	{category: "nvme_timeout", re: regexp.MustCompile(`(?i)nvme.*(timeout|reset|I/O error|critical warning)`)},
	{category: "aer", re: regexp.MustCompile(`AER:|Corrected error|Uncorrected error`)},
	{category: "edac", re: regexp.MustCompile(`EDAC|CE memory read error|UE memory read error`)},
	{category: "thermal", re: regexp.MustCompile(`(?i)thermal.*critical|Core temperature above threshold`)},
	{category: "power", re: regexp.MustCompile(`Power supply|UPS|battery`)},
	{category: "segfault", re: regexp.MustCompile(`segfault at|traps:`)},
	{category: "audit_avc", re: regexp.MustCompile(`avc:  denied|SELinux`)},
	{category: "clock", re: regexp.MustCompile(`clocksource|time drift|adjtimex`)},
	{category: "boot", re: regexp.MustCompile(`Failed to start|dependency failed|emergency mode`)},
	{category: "systemd_unit_failed", re: regexp.MustCompile(`Failed to start|Failed with result|dependency failed|entered failed state|Job .* failed`)},
}

const maxPatternLine = 16 << 20

// scanPatterns 只扫描已采集/已落盘的内容，按行做字面正则匹配。
// 不判断严重性，也不输出根因；file 是包内相对路径，line 是 1-based。
func scanPatterns(ctx context.Context, env *collect.Env, relPaths []string, ref time.Time) []model.LogPattern {
	stats := make([]model.LogPattern, len(patternDefs))
	first := make([]time.Time, len(patternDefs))
	last := make([]time.Time, len(patternDefs))
	hasFirst := make([]bool, len(patternDefs))
	hasLast := make([]bool, len(patternDefs))
	for i := range patternDefs {
		stats[i].Category = patternDefs[i].category
	}

	for _, rel := range relPaths {
		if err := ctx.Err(); err != nil {
			break
		}
		f, err := os.Open(env.Path(rel))
		if err != nil {
			env.AddError("logs", "扫描 %s 失败: %v", rel, err)
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), maxPatternLine)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			line := sc.Text()
			parsedTS := false
			var ts time.Time
			var tsOK bool
			for i := range patternDefs {
				if !patternDefs[i].re.MatchString(line) {
					continue
				}
				p := &stats[i]
				p.Count++
				if len(p.Samples) < 3 {
					// 先截断再清洗：日志行可能很大，避免为每条样本复制整行。
					text := strings.TrimSpace(truncateRunes(line, 500))
					text = strings.ToValidUTF8(text, "\uFFFD")
					p.Samples = append(p.Samples, model.PatternEntry{
						File: rel,
						Line: lineNo,
						Text: text,
					})
				}
				if !parsedTS {
					ts, tsOK = parseTimestamp(line, ref)
					parsedTS = true
				}
				if tsOK {
					if !hasFirst[i] || ts.Before(first[i]) {
						first[i], hasFirst[i] = ts, true
					}
					if !hasLast[i] || ts.After(last[i]) {
						last[i], hasLast[i] = ts, true
					}
				}
			}
		}
		if err := sc.Err(); err != nil {
			env.AddError("logs", "扫描 %s 失败: %v", rel, err)
		}
		_ = f.Close()
	}

	out := make([]model.LogPattern, 0, len(stats))
	for i := range stats {
		if stats[i].Count == 0 {
			continue
		}
		if hasFirst[i] {
			stats[i].FirstTS = formatTimestamp(first[i])
		}
		if hasLast[i] {
			stats[i].LastTS = formatTimestamp(last[i])
		}
		out = append(out, stats[i])
	}
	return out
}

// truncateRunes 按 Unicode 码点截断到 n 个字符，不复制整行（日志行可能很大）。
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}
