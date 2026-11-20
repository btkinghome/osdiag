package logs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
)

func TestParseTimestamp(t *testing.T) {
	ref := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "rfc3339_nano_offset", in: "2026-11-20T06:59:58.123456+00:00 kb kernel: msg", want: "2026-11-20T06:59:58Z", ok: true},
		{name: "rfc3339_z", in: "2026-11-20T06:59:58Z host msg", want: "2026-11-20T06:59:58Z", ok: true},
		{name: "rfc3339_east8", in: "2026-11-20T06:59:58+08:00 host msg", want: "2026-11-19T22:59:58Z", ok: true},
		{name: "rfc3339_east8_no_colon", in: "2026-11-20T06:59:58+0800 host msg", want: "2026-11-19T22:59:58Z", ok: true},
		{name: "rfc3339_comma", in: "2026-11-20T06:59:58,500000Z host msg", want: "2026-11-20T06:59:58Z", ok: true},
		{name: "space_no_zone", in: "2026-11-20 07:00:01 host msg", want: "2026-11-20T07:00:01Z", ok: true},
		{name: "syslog_month", in: "Nov 20 06:59:58 host msg", want: "2026-11-20T06:59:58Z", ok: true},
		{name: "syslog_rollover", in: "Dec 31 23:59:59 host msg", want: "2025-12-31T23:59:59Z", ok: true},
		{name: "pri_prefix", in: "<6>2026-11-20T06:59:58Z host msg", want: "2026-11-20T06:59:58Z", ok: true},
		{name: "kernel_monotonic", in: "<6>[    1.234567] nvme nvme0: timeout", ok: false},
		{name: "garbage", in: "no timestamp here", ok: false},
		{name: "empty", in: "", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseTimestamp(tt.in, ref)
			if ok != tt.ok {
				t.Fatalf("ok=%v want %v", ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if s := formatTimestamp(got); s != tt.want {
				t.Fatalf("timestamp=%s want %s", s, tt.want)
			}
		})
	}
}

func TestParseJournalExport(t *testing.T) {
	data := strings.Join([]string{
		"__CURSOR=s=abc",
		"__REALTIME_TIMESTAMP=1700467200123456",
		"PRIORITY=3",
		"MESSAGE=first line",
		"second line",
		"",
		"__REALTIME_TIMESTAMP=1700467201000000",
		"PRIORITY=6",
		"SYSLOG_IDENTIFIER=sshd",
		"_PID=42",
		"MESSAGE=Accepted publickey for ops",
		"",
		"__REALTIME_TIMESTAMP=not-a-number",
		"MESSAGE=must be skipped",
		"",
		"__REALTIME_TIMESTAMP=1700467202000000",
		"MESSAGE=no priority",
	}, "\n")

	entries := parseJournalExport(data)
	if len(entries) != 3 {
		t.Fatalf("entries=%d want 3", len(entries))
	}
	if got := entries[0].Time.UnixMicro(); got != 1700467200123456 {
		t.Fatalf("first timestamp=%d", got)
	}
	if entries[0].Priority != 3 || entries[0].Message != "first line\nsecond line" {
		t.Fatalf("first entry=%+v", entries[0])
	}
	if entries[1].Ident != "sshd" || entries[1].PID != "42" || entries[1].Priority != 6 {
		t.Fatalf("second entry=%+v", entries[1])
	}
	if entries[2].Priority != -1 || entries[2].Message != "no priority" {
		t.Fatalf("third entry=%+v", entries[2])
	}
	line := journalEntryLine(entries[1])
	if !strings.Contains(line, "[pri=6]") || !strings.Contains(line, "sshd[42]: Accepted publickey") {
		t.Fatalf("line=%q", line)
	}
	if !strings.HasPrefix(line, formatTimestamp(entries[1].Time)) {
		t.Fatalf("line 未以时间戳开头: %q", line)
	}
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("规范化后仍含换行: %q", line)
	}
}

func TestScanPatterns(t *testing.T) {
	stage := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stage, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	lines := []string{
		`2026-11-20T06:59:58.123456+00:00 kb-node-07 kernel: nvme nvme0: I/O 512 QID 3 timeout, aborting`,
		`2026-11-20T06:59:59+00:00 kb-node-07 kernel: bond0: link status down for interface eth2, disabling it in 100 ms`,
		`2026-11-20T07:00:00+00:00 kb-node-07 kernel: bond0: link status up for interface eth2, enabling it in 200 ms`,
		`2026-11-20T07:00:01.250000+00:00 kb-node-07 systemd[1]: backup.service: Failed with result 'exit-code'.`,
		`2026-11-20T07:00:02+00:00 kb-node-07 kernel: Out of memory: Killed process 123 (foo)`,
		`2026-11-20T07:00:03+00:00 kb-node-07 kernel: BUG: ` + strings.Repeat("x", 600),
		`sd 0:0:0:0: [sda] I/O error, dev sda, sector 123`,
		`2026-11-20T07:00:04+00:00 kb-node-07 kernel: BUG: ` + string([]byte{0xff, 0xfe}) + ` invalid utf8`,
	}
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	path := filepath.Join(stage, "logs", "kmsg.log")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	ref := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	env := collect.NewEnv(stage, "run", stage, "quick", ref, time.Minute)
	pats := scanPatterns(context.Background(), env, []string{"logs/kmsg.log"}, ref)
	byCategory := map[string]model.LogPattern{}
	for _, p := range pats {
		byCategory[p.Category] = p
	}

	checks := []struct {
		category string
		count    int
		first    string
		last     string
		line     int
	}{
		{category: "nvme_timeout", count: 1, first: "2026-11-20T06:59:58Z", last: "2026-11-20T06:59:58Z", line: 1},
		{category: "link_flap", count: 2, first: "2026-11-20T06:59:59Z", last: "2026-11-20T07:00:00Z", line: 2},
		{category: "systemd_unit_failed", count: 1, first: "2026-11-20T07:00:01Z", last: "2026-11-20T07:00:01Z", line: 4},
		{category: "oom", count: 1, first: "2026-11-20T07:00:02Z", last: "2026-11-20T07:00:02Z", line: 5},
		{category: "io_error", count: 1, line: 7},
	}
	for _, c := range checks {
		p, ok := byCategory[c.category]
		if !ok {
			t.Fatalf("缺少类别 %s", c.category)
		}
		if p.Count != c.count {
			t.Fatalf("%s count=%d want %d", c.category, p.Count, c.count)
		}
		if p.FirstTS != c.first || p.LastTS != c.last {
			t.Fatalf("%s ts=%q..%q want %q..%q", c.category, p.FirstTS, p.LastTS, c.first, c.last)
		}
		if len(p.Samples) == 0 {
			t.Fatalf("%s 无样本", c.category)
		}
		if p.Samples[0].File != "logs/kmsg.log" || p.Samples[0].Line != c.line {
			t.Fatalf("%s sample=%+v want line %d", c.category, p.Samples[0], c.line)
		}
	}

	oops := byCategory["oops_panic"]
	if oops.Count != 2 {
		t.Fatalf("oops count=%d", oops.Count)
	}
	if n := len([]rune(oops.Samples[0].Text)); n != 500 {
		t.Fatalf("sample text runes=%d want 500", n)
	}
	if !utf8.ValidString(oops.Samples[1].Text) {
		t.Fatalf("sample text 含非法 UTF-8: %q", oops.Samples[1].Text)
	}
}

func TestCopyTail(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.log")
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("line-")
		b.WriteByte(byte('0' + i/10))
		b.WriteByte(byte('0' + i%10))
		b.WriteByte('\n')
	}
	content := b.String()
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("truncated_at_line_boundary", func(t *testing.T) {
		dst := filepath.Join(dir, "tail.log")
		truncated, err := copyTail(src, dst, 50)
		if err != nil {
			t.Fatal(err)
		}
		if !truncated {
			t.Fatal("want truncated")
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) >= len(content) || len(got) > 50 {
			t.Fatalf("tail bytes=%d", len(got))
		}
		if !strings.HasPrefix(string(got), "line-") {
			t.Fatalf("tail 未从行边界开始: %q", string(got)[:min(20, len(got))])
		}
		if !strings.HasSuffix(content, string(got)) {
			t.Fatalf("tail 不是原文件后缀: %q", string(got))
		}
	})

	t.Run("no_truncation", func(t *testing.T) {
		dst := filepath.Join(dir, "full.log")
		truncated, err := copyTail(src, dst, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if truncated {
			t.Fatal("unexpected truncated")
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != content {
			t.Fatalf("copy differs")
		}
	})
}

func TestImportJournalExports(t *testing.T) {
	dir := t.TempDir()
	export := filepath.Join(dir, "export.txt")
	data := strings.Join([]string{
		"__REALTIME_TIMESTAMP=1700467200123456",
		"PRIORITY=3",
		"MESSAGE=first line",
		"second line",
		"",
		"__REALTIME_TIMESTAMP=1700467201000000",
		"SYSLOG_IDENTIFIER=backup.service",
		"PRIORITY=3",
		"MESSAGE=Failed with result 'exit-code'.",
	}, "\n")
	if err := os.WriteFile(export, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	env := collect.NewEnv(stage, "run", stage, "quick", time.Now().UTC(), time.Minute)
	env.JournalFile = export

	rec, ok := importJournalExports(context.Background(), env)
	if !ok {
		t.Fatal("import failed")
	}
	if rec.Path != journalExportRel || rec.Source != "journal-export" || rec.Lines == nil || *rec.Lines != 2 {
		t.Fatalf("record=%+v", rec)
	}
	out, err := os.ReadFile(filepath.Join(stage, journalExportRel))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "first line second line") {
		t.Fatalf("multiline message 未合并: %q", text)
	}
	if !strings.Contains(text, "backup.service: Failed with result") {
		t.Fatalf("ident 未保留: %q", text)
	}
	if strings.Count(text, "\n") != 2 {
		t.Fatalf("lines=%d want 2: %q", strings.Count(text, "\n"), text)
	}
}
