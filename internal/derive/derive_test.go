package derive

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
)

func TestRunDerives(t *testing.T) {
	stage := t.TempDir()
	writeTestFile(t, filepath.Join(stage, "metrics", "streams.json"), `{
  "schema_version": "1.0",
  "streams": [
    {
      "id": "cpu", "path": "metrics/cpu.jsonl", "interval_ms": 1000, "rows": 3,
      "start": "2026-11-20T07:00:00Z", "end": "2026-11-20T07:00:02Z",
      "scope": "cpu", "fields": ["cpu", "idle_pct", "load1", "ctx_switches"],
      "quality": ["ok"]
    },
    {
      "id": "memory", "path": "metrics/memory.jsonl", "interval_ms": 1000, "rows": 3,
      "start": "2026-11-20T07:00:00Z", "end": "2026-11-20T07:00:02Z",
      "scope": "host", "fields": ["mem_total_bytes", "mem_available_bytes"],
      "quality": ["ok"]
    },
    {
      "id": "network", "path": "metrics/network.jsonl", "interval_ms": 1000, "rows": 3,
      "start": "2026-11-20T07:00:00Z", "end": "2026-11-20T07:00:02Z",
      "scope": "iface", "fields": ["iface", "rx_bytes"],
      "quality": ["ok"]
    },
    {
      "id": "disk", "path": "metrics/disk.jsonl", "interval_ms": 1000, "rows": 2,
      "start": "2026-11-20T07:00:00Z", "end": "2026-11-20T07:00:01Z",
      "scope": "disk", "fields": ["device", "write_bytes_total"],
      "quality": ["ok"]
    }
  ]
}`)
	writeTestFile(t, filepath.Join(stage, "metrics", "cpu.jsonl"),
		`{"ts":"2026-11-20T07:00:00Z","cpu":"all","idle_pct":80.0,"load1":0.5,"ctx_switches":100}
{"ts":"2026-11-20T07:00:01Z","cpu":"all","idle_pct":75.0,"load1":0.7,"ctx_switches":150}
{"ts":"2026-11-20T07:00:02Z","cpu":"all","idle_pct":90.0,"load1":null,"ctx_switches":200}
`)
	writeTestFile(t, filepath.Join(stage, "metrics", "memory.jsonl"),
		`{"ts":"2026-11-20T07:00:00Z","mem_total_bytes":1000,"mem_available_bytes":250}
{"ts":"2026-11-20T07:00:01Z","mem_total_bytes":1000,"mem_available_bytes":null}
{"ts":"2026-11-20T07:00:02Z","mem_total_bytes":1000,"mem_available_bytes":500}
`)
	writeTestFile(t, filepath.Join(stage, "metrics", "network.jsonl"),
		`{"ts":"2026-11-20T07:00:00Z","iface":"eth0","rx_bytes":1000000}
{"ts":"2026-11-20T07:00:01Z","iface":"eth0","rx_bytes":1900000}
{"ts":"2026-11-20T07:00:02Z","iface":"eth0","rx_bytes":1700000}
`)
	writeTestFile(t, filepath.Join(stage, "metrics", "disk.jsonl"),
		`{"ts":"2026-11-20T07:00:00Z","device":"nvme0n1","write_bytes_total":5000}
{"ts":"2026-11-20T07:00:01Z","device":"nvme0n1","write_bytes_total":4000}
`)
	writeTestFile(t, filepath.Join(stage, "agent", "field-dictionary.json"), `{
  "schema_version": "1.0",
  "fields": [
    {"name":"idle_pct","stream":"cpu","type":"number","unit":"%","kind":"gauge","scope":"cpu","description":"空闲占比。"},
    {"name":"load1","stream":"cpu","type":"number","unit":"1","kind":"gauge","scope":"host","description":"1 分钟负载。"},
    {"name":"ctx_switches","stream":"cpu","type":"integer","unit":"1","kind":"counter","scope":"host","description":"上下文切换累计。"},
    {"name":"mem_total_bytes","stream":"memory","type":"integer","unit":"bytes","kind":"gauge","scope":"host","description":"总内存。"},
    {"name":"mem_available_bytes","stream":"memory","type":"integer","unit":"bytes","kind":"gauge","scope":"host","description":"可用内存。"},
    {"name":"rx_bytes","stream":"network","type":"integer","unit":"bytes","kind":"counter","scope":"iface","description":"接收字节累计。"},
    {"name":"write_bytes_total","stream":"disk","type":"integer","unit":"bytes","kind":"counter","scope":"disk","description":"写入字节累计。"}
  ]
}`)

	env := collect.NewEnv(stage, "test", stage, "standard",
		time.Date(2026, 11, 20, 7, 0, 0, 0, time.UTC), 2*time.Second)
	if err := Run(env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if errs := env.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}

	// ---- field-stats ----
	var stats model.FieldStatsDoc
	readJSON(t, filepath.Join(stage, "derived", "field-stats.json"), &stats)
	if stats.SchemaVersion != model.SchemaVersion {
		t.Fatalf("schema_version = %q", stats.SchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339, stats.GeneratedAt); err != nil {
		t.Fatalf("generated_at 不是 RFC3339: %q", stats.GeneratedAt)
	}
	load1 := findStat(t, stats, "cpu", "load1")
	if load1.Count != 2 || load1.Nulls != 1 {
		t.Fatalf("load1 count/nulls = %d/%d, want 2/1", load1.Count, load1.Nulls)
	}
	assertPtr(t, "load1.min", load1.Min, 0.5)
	assertPtr(t, "load1.max", load1.Max, 0.7)
	assertPtr(t, "load1.mean", load1.Mean, 0.6)
	assertPtr(t, "load1.p50", load1.P50, 0.5)
	assertPtr(t, "load1.p95", load1.P95, 0.7)
	assertPtr(t, "load1.p99", load1.P99, 0.7)
	if len(load1.Quality) != 1 || load1.Quality[0] != "ok" {
		t.Fatalf("quality = %v, want [ok]", load1.Quality)
	}
	idle := findStat(t, stats, "cpu", "idle_pct")
	if idle.Count != 3 || idle.Nulls != 0 {
		t.Fatalf("idle_pct count/nulls = %d/%d, want 3/0", idle.Count, idle.Nulls)
	}
	assertPtr(t, "idle_pct.p50", idle.P50, 80)

	avail := findStat(t, stats, "memory", "mem_available_bytes")
	if avail.Count != 2 || avail.Nulls != 1 {
		t.Fatalf("mem_available count/nulls = %d/%d, want 2/1", avail.Count, avail.Nulls)
	}
	assertPtr(t, "mem_available.mean", avail.Mean, 375)
	assertPtr(t, "mem_available.p50", avail.P50, 250)

	// ---- deltas ----
	var deltas model.DeltasDoc
	readJSON(t, filepath.Join(stage, "derived", "deltas.json"), &deltas)
	if deltas.IntervalS != 2 {
		t.Fatalf("interval_s = %v, want 2", deltas.IntervalS)
	}
	ctx := findDelta(t, deltas, "cpu", "ctx_switches", "host")
	if ctx.Start != 100 || ctx.End != 200 || ctx.CounterReset {
		t.Fatalf("ctx_switches = %+v", ctx)
	}
	assertPtr(t, "ctx_switches.delta", ctx.Delta, 100)
	assertPtr(t, "ctx_switches.rate", ctx.RatePerS, 50)

	rx := findDelta(t, deltas, "network", "rx_bytes", "eth0")
	if rx.Start != 1000000 || rx.End != 1700000 || rx.CounterReset {
		t.Fatalf("rx_bytes = %+v", rx)
	}
	assertPtr(t, "rx_bytes.delta", rx.Delta, 700000)
	assertPtr(t, "rx_bytes.rate", rx.RatePerS, 350000)

	reset := findDelta(t, deltas, "disk", "write_bytes_total", "nvme0n1")
	if reset.Start != 5000 || reset.End != 4000 || !reset.CounterReset {
		t.Fatalf("reset delta = %+v", reset)
	}
	if reset.Delta != nil || reset.RatePerS != nil {
		t.Fatalf("counter reset 不应输出 delta/rate: %+v", reset)
	}

	// ---- formulas ----
	var formulas model.FormulasDoc
	readJSON(t, filepath.Join(stage, "derived", "formulas.json"), &formulas)
	if len(formulas.Formulas) < 3 {
		t.Fatalf("formulas = %d, want >=3", len(formulas.Formulas))
	}
	names := map[string]bool{}
	for _, f := range formulas.Formulas {
		names[f.Name] = true
	}
	for _, want := range []string{"cpu.util_pct", "memory.available_ratio", "network.rx_mbps"} {
		if !names[want] {
			t.Fatalf("缺少公式 %q", want)
		}
	}

	// ---- series ----
	points := readSeries(t, filepath.Join(stage, "derived", "series.jsonl"))
	byName := map[string][]model.SeriesPoint{}
	for _, p := range points {
		byName[p.Name] = append(byName[p.Name], p)
	}
	assertSeries(t, byName["cpu.util_pct"], []float64{20, 25, 10}, "%", "all")
	assertSeries(t, byName["memory.available_ratio"], []float64{0.25, 0.5}, "1", "host")
	assertSeries(t, byName["network.rx_mbps"], []float64{7.2}, "Mbps", "eth0")
	if len(points) != 6 {
		t.Fatalf("series points = %d, want 6: %+v", len(points), points)
	}
	var prev time.Time
	for _, p := range points {
		ts, err := time.Parse(time.RFC3339, p.TS)
		if err != nil {
			t.Fatalf("series ts 非法: %q", p.TS)
		}
		if ts.Before(prev) {
			t.Fatalf("series 未按 ts 升序: %v 在 %v 之后", ts, prev)
		}
		prev = ts
	}
}

func TestRunCreatesEmptySeriesWithoutRows(t *testing.T) {
	stage := t.TempDir()
	writeTestFile(t, filepath.Join(stage, "metrics", "streams.json"), `{
  "schema_version": "1.0",
  "streams": [
    {"id":"cpu","path":"metrics/cpu.jsonl","interval_ms":1000,"rows":0,
     "start":"2026-11-20T07:00:00Z","end":"2026-11-20T07:00:00Z",
     "scope":"cpu","fields":["idle_pct"],"quality":["ok"]}
  ]
}`)
	writeTestFile(t, filepath.Join(stage, "metrics", "cpu.jsonl"), "")
	writeTestFile(t, filepath.Join(stage, "agent", "field-dictionary.json"), `{
  "schema_version": "1.0",
  "fields": [
    {"name":"idle_pct","stream":"cpu","type":"number","unit":"%","kind":"gauge","scope":"cpu","description":"空闲占比。"}
  ]
}`)

	env := collect.NewEnv(stage, "test", stage, "standard", time.Now(), 0)
	if err := Run(env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var stats model.FieldStatsDoc
	readJSON(t, filepath.Join(stage, "derived", "field-stats.json"), &stats)
	if len(stats.Streams) != 0 {
		t.Fatalf("空流不应产生统计: %+v", stats.Streams)
	}
	var deltas model.DeltasDoc
	readJSON(t, filepath.Join(stage, "derived", "deltas.json"), &deltas)
	if len(deltas.Fields) != 0 {
		t.Fatalf("空流不应产生 deltas: %+v", deltas.Fields)
	}
	b, err := os.ReadFile(filepath.Join(stage, "derived", "series.jsonl"))
	if err != nil {
		t.Fatalf("series.jsonl 应存在: %v", err)
	}
	if len(b) != 0 {
		t.Fatalf("series.jsonl 应为空，got %q", b)
	}
}

func TestRunWithoutStreamsIsNoop(t *testing.T) {
	stage := t.TempDir()
	env := collect.NewEnv(stage, "test", stage, "standard", time.Now(), 0)
	if err := Run(env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage, "derived")); !os.IsNotExist(err) {
		t.Fatalf("M0 场景不应写 derived 目录, err=%v", err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
}

func readSeries(t *testing.T, path string) []model.SeriesPoint {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []model.SeriesPoint
	for _, line := range splitLines(string(b)) {
		if line == "" {
			continue
		}
		var p model.SeriesPoint
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("unmarshal series line %q: %v", line, err)
		}
		out = append(out, p)
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func findStat(t *testing.T, doc model.FieldStatsDoc, stream, field string) model.FieldStat {
	t.Helper()
	for _, st := range doc.Streams {
		if st.Stream == stream && st.Field == field {
			return st
		}
	}
	t.Fatalf("field-stats 缺少 %s.%s", stream, field)
	return model.FieldStat{}
}

func findDelta(t *testing.T, doc model.DeltasDoc, stream, field, scope string) model.Delta {
	t.Helper()
	for _, d := range doc.Fields {
		if d.Stream == stream && d.Field == field && d.Scope == scope {
			return d
		}
	}
	t.Fatalf("deltas 缺少 %s.%s@%s", stream, field, scope)
	return model.Delta{}
}

func assertPtr(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, want)
	}
	if math.Abs(*got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}

func assertSeries(t *testing.T, got []model.SeriesPoint, want []float64, unit, scope string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("series len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, p := range got {
		if math.Abs(p.Value-want[i]) > 1e-9 {
			t.Fatalf("series[%d].value = %v, want %v", i, p.Value, want[i])
		}
		if p.Unit != unit {
			t.Fatalf("series[%d].unit = %q, want %q", i, p.Unit, unit)
		}
		if p.Scope != scope {
			t.Fatalf("series[%d].scope = %q, want %q", i, p.Scope, scope)
		}
	}
}
