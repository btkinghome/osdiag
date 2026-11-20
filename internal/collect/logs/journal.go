package logs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
)

const (
	journalExportRel      = "logs/journal-export.log"
	maxJournalExportBytes = 8 << 20
)

// journalEntry 是 journalctl -o export 文本里的一个记录块。
// 只解析需要的字段；PRIORITY 原样保留用于人工复核。
type journalEntry struct {
	Time     time.Time
	Priority int
	Message  string
	Ident    string
	PID      string
}

// collectJournalInfo 只 stat 二进制 journal 目录，绝不读取文件内容。
func collectJournalInfo(env *collect.Env) model.JournalInfo {
	info := model.JournalInfo{
		Present: false,
		Parsed:  false, // schema const false：不做二进制解析
		Gap:     "journal_binary",
	}
	var files int
	var size int64

	for _, root := range []string{"/run/log/journal", "/var/log/journal"} {
		st, err := os.Stat(root)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				env.AddError("logs", "stat %s 失败: %v", root, err)
			}
			continue
		}
		info.Present = true
		if !st.IsDir() {
			continue
		}
		err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil // 单个子目录不可读不影响其余统计
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			files++
			size += fi.Size()
			return nil
		})
		if err != nil {
			env.AddError("logs", "遍历 %s 失败: %v", root, err)
		}
	}

	info.Files = &files
	info.Bytes = &size
	return info
}

// parseJournalExport 解析 journalctl -o export 的文本块（块之间以空行分隔）。
// 支持 MESSAGE 的多行续行（续行不含 NAME= 字段名）。
func parseJournalExport(data string) []journalEntry {
	data = strings.ReplaceAll(data, "\r\n", "\n")
	var entries []journalEntry
	fields := map[string]string{}
	current := ""

	flush := func() {
		if len(fields) == 0 {
			current = ""
			return
		}
		rawTS := strings.TrimSpace(fields["__REALTIME_TIMESTAMP"])
		usec, err := strconv.ParseInt(rawTS, 10, 64)
		if err == nil {
			e := journalEntry{
				Time:     time.Unix(0, usec*1000).UTC(),
				Priority: -1,
				Message:  strings.TrimRight(fields["MESSAGE"], "\n"),
				Ident:    fields["SYSLOG_IDENTIFIER"],
				PID:      fields["_PID"],
			}
			if p, err := strconv.Atoi(strings.TrimSpace(fields["PRIORITY"])); err == nil {
				e.Priority = p
			}
			entries = append(entries, e)
		}
		fields = map[string]string{}
		current = ""
	}

	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			flush()
			continue
		}
		if i := strings.IndexByte(line, '='); i > 0 && validExportField(line[:i]) {
			current = line[:i]
			fields[current] = line[i+1:]
			continue
		}
		if current != "" {
			fields[current] += "\n" + line
		}
	}
	flush()
	return entries
}

func validExportField(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

// journalEntryLine 把一条记录规范化为单行文本，时间戳放行首，便于统一解析。
func journalEntryLine(e journalEntry) string {
	msg := strings.ToValidUTF8(e.Message, "\uFFFD")
	msg = strings.ReplaceAll(msg, "\n", " ")
	msg = strings.ReplaceAll(msg, "\x00", " ")
	msg = strings.TrimSpace(msg)
	ident := strings.TrimSpace(e.Ident)

	var b strings.Builder
	b.WriteString(formatTimestamp(e.Time))
	if e.Priority >= 0 {
		fmt.Fprintf(&b, " [pri=%d]", e.Priority)
	}
	if ident != "" {
		b.WriteByte(' ')
		b.WriteString(ident)
		if pid := strings.TrimSpace(e.PID); pid != "" {
			fmt.Fprintf(&b, "[%s]", pid)
		}
		b.WriteString(": ")
	} else {
		b.WriteByte(' ')
	}
	b.WriteString(msg)
	return strings.TrimRight(b.String(), " ")
}

// importJournalExports 读取 env.JournalFile（逗号分隔），把 export 文本导入
// 到 logs/journal-export.log。journal.parsed 仍然保持 false。
func importJournalExports(ctx context.Context, env *collect.Env) (*fileRecord, bool) {
	if strings.TrimSpace(env.JournalFile) == "" {
		return nil, false
	}
	dst := env.Path(journalExportRel)

	var out strings.Builder
	anyRead := false
	truncated := false
readLoop:
	for _, raw := range strings.Split(env.JournalFile, ",") {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			env.AddError("logs", "导入 journal 导出被取消: %v", err)
			return nil, false
		}
		data, cut, err := readTailBytes(path, maxJournalExportBytes)
		if err != nil {
			env.AddError("logs", "读取 journal 导出 %s 失败: %v", path, err)
			continue
		}
		anyRead = true
		if cut {
			truncated = true
			// 丢弃从块中间开始的半截记录。
			if i := bytes.Index(data, []byte("\n\n")); i >= 0 {
				data = data[i+2:]
			} else {
				data = nil
			}
		}
		for _, e := range parseJournalExport(string(data)) {
			line := journalEntryLine(e) + "\n"
			// 多个导出文件合并后仍受单文件 8MiB 上限约束。
			if int64(out.Len()+len(line)) > maxJournalExportBytes {
				truncated = true
				break readLoop
			}
			out.WriteString(line)
		}
	}
	if !anyRead {
		return nil, false
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		env.AddError("logs", "创建 logs 目录失败: %v", err)
		return nil, false
	}
	if err := os.WriteFile(dst, []byte(out.String()), 0o644); err != nil {
		env.AddError("logs", "写 %s 失败: %v", journalExportRel, err)
		return nil, false
	}
	rec, err := describeFile(env, dst, journalExportRel, "journal-export", truncated, false)
	if err != nil {
		env.AddError("logs", "统计 %s 失败: %v", journalExportRel, err)
		return nil, false
	}
	return rec, true
}

// readTailBytes 读取文件尾部最多 limit 字节；不读取超过上限的内容。
func readTailBytes(path string, limit int64) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !st.Mode().IsRegular() {
		return nil, false, fmt.Errorf("不是普通文件")
	}
	truncated := st.Size() > limit
	if truncated {
		if _, err := f.Seek(st.Size()-limit, io.SeekStart); err != nil {
			return nil, false, err
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, false, err
	}
	return data, truncated, nil
}
