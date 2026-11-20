package probes

import (
	"math"
	"testing"
)

func TestJudgeLimit(t *testing.T) {
	const u = ^uint64(0)
	cases := []struct {
		limit   string
		cur     uint64
		wantOK  bool
		wantMin int64
	}{
		{"nofile", 1024, true, 1024},
		{"nofile", 1023, false, 1024},
		{"nofile", u, true, 1024},
		{"nofile", math.MaxInt64, true, 1024},
		{"nproc", 256, true, 256},
		{"nproc", 255, false, 256},
		{"as", 512 << 20, true, 512 << 20},
		{"as", 512<<20 - 1, false, 512 << 20},
		{"as", u, true, 512 << 20},
		{"core", 0, true, 0},
		{"unknown", 5, true, 0},
	}
	for _, tc := range cases {
		ok, minSoft := judgeLimit(tc.limit, tc.cur)
		if ok != tc.wantOK || minSoft != tc.wantMin {
			t.Errorf("judgeLimit(%q,%d) = (%v,%d), want (%v,%d)",
				tc.limit, tc.cur, ok, minSoft, tc.wantOK, tc.wantMin)
		}
	}
}

func TestEncodeLimit(t *testing.T) {
	cases := []struct {
		in   uint64
		want int64
	}{
		{0, 0},
		{1024, 1024},
		{512 << 20, 512 << 20},
		{^uint64(0), -1},
		{math.MaxInt64, -1},
		{math.MaxInt64 + 1, math.MaxInt64},
	}
	for _, tc := range cases {
		if got := encodeLimit(tc.in); got != tc.want {
			t.Errorf("encodeLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestEvaluateLimits(t *testing.T) {
	readings := []limitReading{
		{Name: "nofile", Cur: 4096, Max: 1048576},
		{Name: "nproc", Cur: 100, Max: 100},
		{Name: "as", Cur: 0, Max: 0},
		{Name: "core", Cur: 0, Max: 0},
	}
	verdicts, allOK := evaluateLimits(readings)
	if allOK {
		t.Fatal("nproc 不足且 as 不足时应 allOK=false")
	}
	if len(verdicts) != 4 {
		t.Fatalf("verdicts = %d, want 4", len(verdicts))
	}
	byName := map[string]limitVerdict{}
	for _, v := range verdicts {
		byName[v.Name] = v
	}
	if !byName["nofile"].Adequate || byName["nofile"].Soft != 4096 {
		t.Errorf("nofile verdict = %+v", byName["nofile"])
	}
	if byName["nproc"].Adequate {
		t.Errorf("nproc=100 不应判定为充足")
	}
	if byName["as"].Adequate {
		t.Errorf("as=0 不应判定为充足")
	}
	if !byName["core"].Adequate {
		t.Errorf("core=0 应视为正常")
	}

	errReadings := []limitReading{{Name: "nofile", Errno: "EPERM"}}
	verdicts, allOK = evaluateLimits(errReadings)
	if allOK {
		t.Error("getrlimit 失败时 allOK 应为 false")
	}
	if verdicts[0].Adequate {
		t.Error("getrlimit 失败的限额不应判定为充足")
	}
}
