package logs

import (
	"regexp"
	"strings"
	"time"
)

var (
	// 2026-11-20T06:59:58.123456+00:00 / 2026-11-20 06:59:58 / 2026-11-20T06:59:58Z
	isoTimeRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d{1,9})?(?:Z|[+-]\d{2}:?\d{2})?)`)
	// Nov 20 06:59:58（传统 syslog，无年份）
	syslogTimeRe = regexp.MustCompile(`^([A-Z][a-z]{2} {1,2}\d{1,2} \d{2}:\d{2}:\d{2})`)
)

// parseTimestamp 解析行首常见时间戳。ref 用于补全传统 syslog 的年份；
// 解析失败返回 false，调用方应把对应字段留空。
func parseTimestamp(raw string, ref time.Time) (time.Time, bool) {
	s := strings.TrimSpace(raw)
	// 某些日志带 <pri> 前缀，先剥掉（最多 5 位，避免误伤正文）。
	for len(s) > 1 && s[0] == '<' {
		i := strings.IndexByte(s, '>')
		if i < 2 || i > 5 {
			break
		}
		s = strings.TrimSpace(s[i+1:])
	}

	if m := isoTimeRe.FindString(s); m != "" {
		norm := strings.Replace(m, " ", "T", 1)
		norm = strings.Replace(norm, ",", ".", 1)
		if t, err := time.Parse(time.RFC3339Nano, norm); err == nil {
			return t.UTC(), true
		}
		for _, layout := range []string{
			"2006-01-02T15:04:05.999999999",
			"2006-01-02T15:04:05",
			"2006-01-02T15:04:05.999999999-0700",
			"2006-01-02T15:04:05-0700",
		} {
			if t, err := time.ParseInLocation(layout, norm, time.UTC); err == nil {
				return t.UTC(), true
			}
		}
	}

	if m := syslogTimeRe.FindString(s); m != "" {
		t, err := time.ParseInLocation("Jan _2 15:04:05", m, time.UTC)
		if err != nil {
			return time.Time{}, false
		}
		if ref.IsZero() {
			ref = time.Now().UTC()
		}
		out := time.Date(ref.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
		// 12 月日志在 1 月被读到：归到上一年。
		if out.After(ref.Add(24 * time.Hour)) {
			out = out.AddDate(-1, 0, 0)
		}
		return out, true
	}

	return time.Time{}, false
}

func formatTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
