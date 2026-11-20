package runid

import (
	"testing"
	"time"
)

func TestNewMatchesSchemaPattern(t *testing.T) {
	now := time.Date(2026, 11, 20, 7, 0, 0, 0, time.UTC)
	id := New(now)
	if !Pattern.MatchString(id) {
		t.Fatalf("runid %q 不匹配 schema 正则", id)
	}
	if id[:16] != "20261120T070000Z" {
		t.Fatalf("时间前缀错误: %s", id)
	}
	_ = New(now) // 只要求可重复生成，不做唯一性断言
}
