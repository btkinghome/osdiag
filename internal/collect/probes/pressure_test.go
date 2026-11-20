package probes

import (
	"strings"
	"testing"
)

func TestEvaluatePressure(t *testing.T) {
	cases := []struct {
		name       string
		p          pressureSample
		wantSkip   bool
		wantSubstr []string
	}{
		{
			name: "指标都不可用",
			p:    pressureSample{},
		},
		{
			name: "load1 恰好等于阈值不跳过",
			p:    pressureSample{Load1: 8, LoadOK: true},
		},
		{
			name:       "load1 超过 2*ncpu",
			p:          pressureSample{Load1: 8.5, LoadOK: true},
			wantSkip:   true,
			wantSubstr: []string{"load1=8.50", "2*ncpu=8.0"},
		},
		{
			name: "PSI 恰好 30 不跳过",
			p:    pressureSample{PSIAvg10: 30, PSIOK: true},
		},
		{
			name:       "PSI 超过 30",
			p:          pressureSample{PSIAvg10: 31.2, PSIOK: true},
			wantSkip:   true,
			wantSubstr: []string{"psi_io_avg10=31.2"},
		},
		{
			name: "MemAvailable 恰好 512MiB 不跳过",
			p:    pressureSample{MemAvailableBytes: 512 << 20, MemOK: true},
		},
		{
			name:       "MemAvailable 低于 512MiB",
			p:          pressureSample{MemAvailableBytes: 511 << 20, MemOK: true},
			wantSkip:   true,
			wantSubstr: []string{"MemAvailable=511MiB"},
		},
		{
			name: "三项同时超限",
			p: pressureSample{
				Load1: 100, LoadOK: true,
				PSIAvg10: 99, PSIOK: true,
				MemAvailableBytes: 1 << 20, MemOK: true,
			},
			wantSkip:   true,
			wantSubstr: []string{"load1", "psi_io_avg10", "MemAvailable"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skip, reason := evaluatePressure(tc.p, 4)
			if skip != tc.wantSkip {
				t.Fatalf("skip = %v, want %v (reason=%q)", skip, tc.wantSkip, reason)
			}
			for _, sub := range tc.wantSubstr {
				if !strings.Contains(reason, sub) {
					t.Errorf("reason %q 缺少 %q", reason, sub)
				}
			}
		})
	}
}

func TestParsePressureInputs(t *testing.T) {
	if v, ok := parseLoad1("1.75 0.80 0.60 2/345 6789\n"); !ok || v != 1.75 {
		t.Errorf("parseLoad1 = (%v,%v), want (1.75,true)", v, ok)
	}
	if _, ok := parseLoad1(""); ok {
		t.Error("parseLoad1(空) 应失败")
	}

	meminfo := "MemTotal:       16384000 kB\nMemFree:         1000000 kB\nMemAvailable:    4096000 kB\n"
	if v, ok := parseMemAvailable(meminfo); !ok || v != 4096000*1024 {
		t.Errorf("parseMemAvailable = (%d,%v), want %d", v, ok, uint64(4096000*1024))
	}
	if _, ok := parseMemAvailable("MemFree: 1 kB\n"); ok {
		t.Error("缺少 MemAvailable 应失败")
	}

	psi := "some avg10=12.34 avg60=1.00 avg300=0.50 total=12345\nfull avg10=45.60 avg60=2.00 avg300=0.10 total=999\n"
	if v, ok := parsePSIIOAvg10(psi); !ok || v != 45.6 {
		t.Errorf("parsePSIIOAvg10 = (%v,%v), want (45.6,true)", v, ok)
	}
	if _, ok := parsePSIIOAvg10("cpu some avg10=1.0\n"); ok {
		t.Error("非 io 行应失败")
	}
}
