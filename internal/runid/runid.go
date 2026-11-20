// Package runid 生成可排序、可读的运行 ID。
package runid

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"time"
)

// Pattern 与 manifest.schema.json 的 run.id 正则一致。
var Pattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}$`)

// New 返回形如 20261120T070000Z-a1b2c3 的运行 ID。
func New(t time.Time) string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return t.UTC().Format("20060102T150405Z") + "-000000"
	}
	return t.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b)
}
