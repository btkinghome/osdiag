package probes

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
)

func specByLevel(level string) probeSpec {
	for _, s := range probeSpecs() {
		if s.level == level {
			return s
		}
	}
	panic("no spec for level " + level)
}

func TestSkipReason(t *testing.T) {
	overloaded := pressureSample{Load1: 100, LoadOK: true}
	cases := []struct {
		name            string
		level           string
		noProbe         bool
		allowHeavy      bool
		allowExtern     bool
		pressureOn      bool
		press           pressureSample
		cancelCtx       bool
		wantSkip        bool
		wantPressure    bool
		wantReasonParts []string
	}{
		{
			name:  "no_probe 关闭全部",
			level: "P0", noProbe: true,
			wantSkip: true, wantReasonParts: []string{"no_probe"},
		},
		{
			name:  "默认 P0 执行",
			level: "P0",
		},
		{
			name:     "P1 需要 allow-heavy",
			level:    "P1",
			wantSkip: true, wantReasonParts: []string{"--allow-heavy"},
		},
		{
			name:  "P2 需要 allow-heavy",
			level: "P2", allowExtern: true,
			wantSkip: true, wantReasonParts: []string{"--allow-heavy"},
		},
		{
			name:  "P2 需要 allow-external",
			level: "P2", allowHeavy: true,
			wantSkip: true, wantReasonParts: []string{"--allow-external"},
		},
		{
			name:  "P2 开关齐全",
			level: "P2", allowHeavy: true, allowExtern: true,
		},
		{
			name:  "压力保护跳过 P1",
			level: "P1", allowHeavy: true,
			pressureOn: true, press: overloaded,
			wantSkip: true, wantPressure: true, wantReasonParts: []string{"压力保护", "load1"},
		},
		{
			name:  "压力保护不影响 P0",
			level: "P0", allowHeavy: true,
			pressureOn: true, press: overloaded,
		},
		{
			name:  "压力指标缺失不跳过",
			level: "P1", allowHeavy: true,
			pressureOn: true, press: pressureSample{},
		},
		{
			name:  "context 取消时跳过",
			level: "P0", cancelCtx: true,
			wantSkip: true, wantReasonParts: []string{"取消"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := collect.NewEnv(t.TempDir(), "test", t.TempDir(), "quick", time.Now(), 30*time.Second)
			env.NoProbe = tc.noProbe
			env.AllowHeavy = tc.allowHeavy
			env.AllowExtern = tc.allowExtern
			ctx := context.Background()
			if tc.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			reason, skip, dueToPressure := skipReason(ctx, env, specByLevel(tc.level), tc.press, tc.pressureOn, 4)
			if skip != tc.wantSkip {
				t.Fatalf("skip = %v, want %v (reason=%q)", skip, tc.wantSkip, reason)
			}
			if dueToPressure != tc.wantPressure {
				t.Errorf("dueToPressure = %v, want %v", dueToPressure, tc.wantPressure)
			}
			for _, part := range tc.wantReasonParts {
				if !strings.Contains(reason, part) {
					t.Errorf("reason %q 缺少 %q", reason, part)
				}
			}
		})
	}
}

func TestRunNoProbeWritesSkippedResults(t *testing.T) {
	stage := t.TempDir()
	env := collect.NewEnv(stage, "20261120T000000Z-test", t.TempDir(), "quick", time.Now(), 30*time.Second)
	env.NoProbe = true

	if err := Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := env.EnabledProbes(); len(got) != 0 {
		t.Errorf("no_probe 下 EnabledProbes = %v, want 空", got)
	}

	specs := probeSpecs()
	for _, spec := range specs {
		path := filepath.Join(stage, "probes", spec.id+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s: %v", path, err)
		}
		var res model.ProbeResult
		if err := json.Unmarshal(data, &res); err != nil {
			t.Fatalf("解析 %s: %v", path, err)
		}
		if res.SchemaVersion != model.SchemaVersion {
			t.Errorf("%s schema_version = %q", spec.id, res.SchemaVersion)
		}
		if res.Probe != spec.id {
			t.Errorf("%s probe 字段 = %q，必须与文件名一致", spec.id, res.Probe)
		}
		if res.Level != spec.level {
			t.Errorf("%s level = %q, want %q", spec.id, res.Level, spec.level)
		}
		if !res.Skipped || res.OK {
			t.Errorf("%s 应 skipped=true 且 ok=false，得到 skipped=%v ok=%v", spec.id, res.Skipped, res.OK)
		}
		if res.SkipReason == "" || !strings.Contains(res.SkipReason, "no_probe") {
			t.Errorf("%s skip_reason = %q", spec.id, res.SkipReason)
		}
		if len([]rune(res.SkipReason)) > 300 {
			t.Errorf("%s skip_reason 超过 300 字符", spec.id)
		}
		if res.Error != nil {
			t.Errorf("%s skipped 结果不应带 error: %+v", spec.id, res.Error)
		}
		if res.StartedAt == "" {
			t.Errorf("%s started_at 为空", spec.id)
		}
	}

	logData, err := os.ReadFile(filepath.Join(stage, "probes", "runlog.jsonl"))
	if err != nil {
		t.Fatalf("读取 runlog: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if len(lines) != len(specs) {
		t.Fatalf("runlog 行数 = %d, want %d", len(lines), len(specs))
	}
	for i, line := range lines {
		var ev model.RunlogEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("第 %d 行不是合法 JSON: %v", i+1, err)
		}
		if ev.SchemaVersion != model.SchemaVersion {
			t.Errorf("第 %d 行 schema_version = %q", i+1, ev.SchemaVersion)
		}
		if ev.Event != "skip" {
			t.Errorf("第 %d 行 event = %q, want skip", i+1, ev.Event)
		}
		if ev.Probe != specs[i].id || ev.Level != specs[i].level {
			t.Errorf("第 %d 行 probe/level = %q/%q, want %q/%q", i+1, ev.Probe, ev.Level, specs[i].id, specs[i].level)
		}
		if ev.Detail == "" {
			t.Errorf("第 %d 行 skip 事件缺少 detail", i+1)
		}
		if ev.TS == "" {
			t.Errorf("第 %d 行缺少 ts", i+1)
		}
	}
}

func TestRunPolicyGating(t *testing.T) {
	stage := t.TempDir()
	env := collect.NewEnv(stage, "20261120T000000Z-gate", t.TempDir(), "quick", time.Now(), 30*time.Second)
	if err := Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	enabled := map[string]bool{}
	for _, id := range env.EnabledProbes() {
		enabled[id] = true
	}
	for _, spec := range probeSpecs() {
		data, err := os.ReadFile(filepath.Join(stage, "probes", spec.id+".json"))
		if err != nil {
			t.Fatalf("读取 %s: %v", spec.id, err)
		}
		var res model.ProbeResult
		if err := json.Unmarshal(data, &res); err != nil {
			t.Fatalf("解析 %s: %v", spec.id, err)
		}
		switch spec.level {
		case "P0":
			if !enabled[spec.id] {
				t.Errorf("%s 应在默认策略下执行", spec.id)
			}
			if res.Skipped {
				t.Errorf("%s 不应被跳过: %q", spec.id, res.SkipReason)
			}
		case "P1", "P2":
			if enabled[spec.id] {
				t.Errorf("%s 未开启重项却执行了", spec.id)
			}
			if !res.Skipped || !strings.Contains(res.SkipReason, "allow_heavy") {
				t.Errorf("%s 应因 allow_heavy=false 跳过，得到 %+v", spec.id, res)
			}
		}
	}
}
