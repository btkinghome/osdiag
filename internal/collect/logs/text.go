package logs

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
)

const textMaxBytes = 8 << 20

type fileRecord = model.LogFile

type textCandidate struct {
	source  string // schema source 枚举
	src     string // 宿主机只读路径
	relPath string // 包内相对路径
	rotated bool
}

// 按固定顺序尝试；不存在就跳过，不产生错误。
var textCandidates = []textCandidate{
	{source: "messages", src: "/var/log/messages", relPath: "logs/messages.log"},
	{source: "messages", src: "/var/log/messages.1", relPath: "logs/messages.1.log", rotated: true},
	{source: "syslog", src: "/var/log/syslog", relPath: "logs/syslog.log"},
	{source: "syslog", src: "/var/log/syslog.1", relPath: "logs/syslog.1.log", rotated: true},
	{source: "kern", src: "/var/log/kern.log", relPath: "logs/kern.log"},
	{source: "kern", src: "/var/log/kern.log.1", relPath: "logs/kern.1.log", rotated: true},
	{source: "dmesg", src: "/var/log/dmesg", relPath: "logs/dmesg.log"},
	{source: "dmesg", src: "/var/log/dmesg.1", relPath: "logs/dmesg.1.log", rotated: true},
	{source: "boot", src: "/var/log/boot.log", relPath: "logs/boot.log"},
	{source: "boot", src: "/var/log/boot.log.1", relPath: "logs/boot.1.log", rotated: true},
}

// collectTextLog 复制一个文本日志的尾部并生成索引条目。
// 文件不存在时返回 (nil,false) 且不记录错误。
func collectTextLog(env *collect.Env, c textCandidate) (*fileRecord, bool) {
	info, err := os.Stat(c.src)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			env.AddError("logs", "stat %s 失败: %v", c.src, err)
		}
		return nil, false
	}
	if !info.Mode().IsRegular() {
		return nil, false
	}

	dst := env.Path(c.relPath)
	truncated, err := copyTail(c.src, dst, textMaxBytes)
	if err != nil {
		env.AddError("logs", "复制 %s 失败: %v", c.src, err)
		_ = os.Remove(dst)
		return nil, false
	}
	rec, err := describeFile(env, dst, c.relPath, c.source, truncated, c.rotated)
	if err != nil {
		env.AddError("logs", "统计 %s 失败: %v", c.relPath, err)
		return nil, false
	}
	return rec, true
}

// copyTail 把 src 的尾部（最多 limit 字节）复制到 dst；超过 limit 时从
// 行边界开始，保证目标文件首行不是半行。返回是否发生了截断。
func copyTail(src, dst string, limit int64) (bool, error) {
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()

	st, err := in.Stat()
	if err != nil {
		return false, err
	}
	if !st.Mode().IsRegular() {
		return false, fmt.Errorf("不是普通文件")
	}

	truncated := st.Size() > limit
	var r io.Reader = in
	if truncated {
		if _, err := in.Seek(st.Size()-limit, io.SeekStart); err != nil {
			return false, err
		}
		// 丢弃截断窗口里的第一段（很可能是半行）。bufio 会预读，
		// 后续必须继续从 br 读，不能回到 in。
		br := bufio.NewReaderSize(in, 64*1024)
		if _, err := br.ReadString('\n'); err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		r = br
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	out, err := os.Create(dst)
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return false, err
	}
	if err := out.Close(); err != nil {
		return false, err
	}
	return truncated, nil
}

// describeFile 计算已落盘文件的字节数、行数、首末时间和 sha256。
func describeFile(env *collect.Env, abs, rel, source string, truncated, rotated bool) (*fileRecord, error) {
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	lines, first, last, err := analyzeLogFile(abs, env.Start)
	if err != nil {
		return nil, err
	}
	sum, err := sha256File(abs)
	if err != nil {
		return nil, err
	}
	rot := rotated
	return &fileRecord{
		Path:      rel,
		Source:    source,
		Bytes:     st.Size(),
		Lines:     &lines,
		Truncated: truncated,
		Rotated:   &rot,
		FirstTS:   first,
		LastTS:    last,
		SHA256:    sum,
	}, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// analyzeLogFile 按与 validator 的 splitlines 一致的方式统计行数（1-based），
// 并取第一个/最后一个能解析出行首时间的行。解析不了的时间戳留空。
func analyzeLogFile(path string, ref time.Time) (lines int, first, last string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", "", err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, readErr := r.ReadString('\n')
		if len(line) > 0 {
			lines++
			text := strings.TrimRight(line, "\r\n")
			if t, ok := parseTimestamp(text, ref); ok {
				ts := formatTimestamp(t)
				if first == "" {
					first = ts
				}
				last = ts
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return lines, first, last, readErr
		}
	}
	return lines, first, last, nil
}
