package metrics

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
)

type fakeCollector struct {
	setupN  int
	setupEr error
	rows    int
}

func (f *fakeCollector) setup() (int, error) { return f.setupN, f.setupEr }

func (f *fakeCollector) sample(time.Time) ([]row, error) {
	out := make([]row, 0, f.rows)
	for i := 0; i < f.rows; i++ {
		out = append(out, row{"a": i, "b": "x"})
	}
	return out, nil
}

func TestRunStreamWritesJSONLAndBounds(t *testing.T) {
	stage := t.TempDir()
	start := time.Now()
	env := collect.NewEnv(stage, "run-1", stage, "quick", start, time.Second)
	spec := &streamDef{
		id: "fake", scope: "host", fields: []string{"a", "b"},
		newCollector: func(time.Duration) collector { return &fakeCollector{setupN: 2, rows: 2} },
	}
	deadline := start.Add(55 * time.Millisecond)
	windowStart := start.UTC().Format(time.RFC3339)
	windowEnd := deadline.UTC().Format(time.RFC3339)
	res := runStream(context.Background(), env, spec, 10*time.Millisecond, start, deadline, windowStart, windowEnd)
	if res.unsupported || res.skipped {
		t.Fatalf("不应降级: %+v", res)
	}
	if res.rows == 0 || res.rows%2 != 0 {
		t.Fatalf("行数应为实体数的整数倍且非 0，得到 %d", res.rows)
	}
	if res.start != res.firstTS || res.end != res.lastTS {
		t.Fatalf("start/end 必须等于首末行 ts: %q %q / %q %q", res.start, res.end, res.firstTS, res.lastTS)
	}

	f, err := os.Open(env.Path(res.path))
	if err != nil {
		t.Fatalf("打开 JSONL 失败: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	lines := 0
	firstTS, lastTS := "", ""
	for sc.Scan() {
		var r map[string]any
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("第 %d 行不是合法 JSON: %v", lines+1, err)
		}
		lines++
		ts, _ := r["ts"].(string)
		if _, err := time.Parse(time.RFC3339, ts); err != nil {
			t.Fatalf("ts 不是 RFC3339: %q", ts)
		}
		if r["a"] == nil || r["b"] != "x" {
			t.Fatalf("字段缺失: %v", r)
		}
		if firstTS == "" {
			firstTS = ts
		}
		lastTS = ts
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("读取 JSONL 失败: %v", err)
	}
	if lines != res.rows {
		t.Fatalf("文件 %d 行，记录 rows=%d", lines, res.rows)
	}
	if firstTS != res.start || lastTS != res.end {
		t.Fatalf("文件首末 ts 与流元数据不一致")
	}
	if got := res.quality(); len(got) == 0 || got[0] != "ok" {
		t.Fatalf("正常流 quality 应以 ok 开头，得到 %v", got)
	}
}

func TestRunStreamUnsupportedAndSkip(t *testing.T) {
	stage := t.TempDir()
	env := collect.NewEnv(stage, "run-2", stage, "quick", time.Now(), time.Second)
	start := time.Now()
	deadline := start.Add(10 * time.Millisecond)
	ws, we := start.UTC().Format(time.RFC3339), deadline.UTC().Format(time.RFC3339)

	unsupported := &streamDef{
		id: "nope", scope: "host", fields: []string{"x"},
		newCollector: func(time.Duration) collector {
			return &fakeCollector{setupEr: &unsupportedError{note: "能力缺失"}}
		},
	}
	res := runStream(context.Background(), env, unsupported, 10*time.Millisecond, start, deadline, ws, we)
	if !res.unsupported {
		t.Fatalf("应标记 unsupported: %+v", res)
	}
	if res.start != ws || res.end != we {
		t.Fatalf("空流 start/end 应回退到窗口边界")
	}
	if got := res.quality(); len(got) != 1 || got[0] != "unsupported" {
		t.Fatalf("quality = %v，期望 [unsupported]", got)
	}
	st, err := os.Stat(env.Path(res.path))
	if err != nil || st.Size() != 0 {
		t.Fatalf("应存在空的 JSONL 文件: %v size=%v", err, st)
	}

	skipped := &streamDef{
		id: "skip", scope: "net", fields: []string{"x"},
		newCollector: func(time.Duration) collector { return &fakeCollector{setupEr: errSkipStream} },
	}
	res = runStream(context.Background(), env, skipped, 10*time.Millisecond, start, deadline, ws, we)
	if !res.skipped {
		t.Fatalf("应标记 skipped: %+v", res)
	}
	if _, err := os.Stat(env.Path(res.path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("跳过的流不应写文件，err=%v", err)
	}
}

// TestRunEndToEnd 校验编排产物结构与 JSONL 行数一致；在无 /proc 的
// 开发机上同样应完成并给出 unsupported 降级，而不是让 run 失败。
func TestRunEndToEnd(t *testing.T) {
	stage := t.TempDir()
	start := time.Now().UTC().Add(-time.Millisecond)
	env := collect.NewEnv(stage, "run-3", stage, "quick", start, 0)
	if err := Run(context.Background(), env); err != nil {
		t.Fatalf("Run 不应返回错误: %v", err)
	}

	raw, err := os.ReadFile(env.Path("metrics/streams.json"))
	if err != nil {
		t.Fatalf("读取 streams.json: %v", err)
	}
	var index model.StreamIndex
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatalf("解析 streams.json: %v", err)
	}
	if len(index.Streams) == 0 {
		t.Fatal("streams 不应为空")
	}
	seen := map[string]bool{}
	for _, m := range index.Streams {
		seen[m.ID] = true
		if m.Path != "metrics/"+m.ID+".jsonl" {
			t.Errorf("%s 的 path 不规范: %s", m.ID, m.Path)
		}
		if len(m.Quality) == 0 {
			t.Errorf("%s 缺少 quality", m.ID)
		}
		if m.IntervalMS != 1000 {
			t.Errorf("%s interval_ms=%d，quick 应为 1000", m.ID, m.IntervalMS)
		}
		f, err := os.Open(env.Path(m.Path))
		if err != nil {
			t.Errorf("%s 的 JSONL 不存在: %v", m.ID, err)
			continue
		}
		sc := bufio.NewScanner(f)
		lines := 0
		for sc.Scan() {
			lines++
		}
		f.Close()
		if lines != m.Rows {
			t.Errorf("%s 实际 %d 行，streams.json rows=%d", m.ID, lines, m.Rows)
		}
	}
	if !seen["pressure"] {
		t.Error("pressure 流必须始终登记（缺失时降级为 unsupported）")
	}

	dictRaw, err := os.ReadFile(env.Path("agent/field-dictionary.json"))
	if err != nil {
		t.Fatalf("读取字段字典: %v", err)
	}
	var dict model.FieldDictionary
	if err := json.Unmarshal(dictRaw, &dict); err != nil {
		t.Fatalf("解析字段字典: %v", err)
	}
	byStream := map[string]map[string]bool{}
	for _, fd := range dict.Fields {
		if byStream[fd.Stream] == nil {
			byStream[fd.Stream] = map[string]bool{}
		}
		byStream[fd.Stream][fd.Name] = true
	}
	for _, m := range index.Streams {
		for _, f := range m.Fields {
			if !byStream[m.ID][f] {
				t.Errorf("流 %s 字段 %s 缺少字典项", m.ID, f)
			}
		}
	}
	cov := env.Coverage()
	if cov.Ratio < 0 || cov.Ratio > 1 {
		t.Errorf("coverage ratio 越界: %v", cov.Ratio)
	}
}
