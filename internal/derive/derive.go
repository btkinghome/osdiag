// Package derive 生成 L1 派生数据。
//
// L1 只做明确公式的算术（增量、速率、占比、分位），不做任何研判。
// 输入为采集阶段落盘的 metrics/streams.json、metrics/*.jsonl 与
// agent/field-dictionary.json；输出 derived/ 下的四个产物。
package derive

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
	"luanbird/osdiag/internal/pack"
)

// streamData 是一个已加载的指标流。
type streamData struct {
	meta model.StreamMeta
	rows []metricRow
}

// metricRow 是一行 JSONL。ts 保留原始字符串，at 仅用于排序/求间隔。
type metricRow struct {
	ts      string
	at      time.Time
	hasTime bool
	vals    map[string]any
	order   int
}

// dictIndex 以 (stream, field) 建立字段字典索引。
type dictIndex map[string]map[string]model.FieldDef

// Run 生成 L1 派生产物。metrics/streams.json 不存在时视为 M0 场景，
// 不写任何 derived 文件直接返回。
func Run(env *collect.Env) error {
	raw, err := os.ReadFile(env.Path("metrics/streams.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取 metrics/streams.json: %w", err)
	}
	var index model.StreamIndex
	if err := json.Unmarshal(raw, &index); err != nil {
		return fmt.Errorf("解析 metrics/streams.json: %w", err)
	}

	dict, err := loadDictionary(env.Path("agent/field-dictionary.json"))
	if err != nil {
		return err
	}

	var streams []streamData
	for _, meta := range index.Streams {
		rows, err := readMetricRows(env.Path(meta.Path))
		if err != nil {
			// 单个流读取失败不应拖垮整包派生；记录下来由上层标记 partial。
			env.AddError("derive", "读取 %s: %v", meta.Path, err)
			continue
		}
		streams = append(streams, streamData{meta: meta, rows: rows})
	}

	stats := buildFieldStats(streams, dict)
	if err := pack.WriteJSON(env.Path("derived/field-stats.json"), stats); err != nil {
		return fmt.Errorf("写 derived/field-stats.json: %w", err)
	}

	deltas := buildDeltas(streams, dict, env.Duration)
	if err := pack.WriteJSON(env.Path("derived/deltas.json"), deltas); err != nil {
		return fmt.Errorf("写 derived/deltas.json: %w", err)
	}

	formulas := buildFormulas()
	if err := pack.WriteJSON(env.Path("derived/formulas.json"), formulas); err != nil {
		return fmt.Errorf("写 derived/formulas.json: %w", err)
	}

	series := buildSeries(streams, dict)
	if err := writeSeries(env.Path("derived/series.jsonl"), series); err != nil {
		return fmt.Errorf("写 derived/series.jsonl: %w", err)
	}
	return nil
}

func loadDictionary(path string) (model.FieldDictionary, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return model.FieldDictionary{SchemaVersion: model.SchemaVersion}, nil
		}
		return model.FieldDictionary{}, fmt.Errorf("读取 agent/field-dictionary.json: %w", err)
	}
	var dict model.FieldDictionary
	if err := json.Unmarshal(raw, &dict); err != nil {
		return model.FieldDictionary{}, fmt.Errorf("解析 agent/field-dictionary.json: %w", err)
	}
	return dict, nil
}

func indexDictionary(dict model.FieldDictionary) dictIndex {
	idx := make(dictIndex, len(dict.Fields))
	for _, def := range dict.Fields {
		if idx[def.Stream] == nil {
			idx[def.Stream] = map[string]model.FieldDef{}
		}
		idx[def.Stream][def.Name] = def
	}
	return idx
}

// lookupDef 返回字段定义；字典缺项时返回以流 scope 兜底的零值定义，
// 使 scope 解析与输出仍可继续。
func lookupDef(dict model.FieldDictionary, stream, field string, streamScope string) model.FieldDef {
	for _, def := range dict.Fields {
		if def.Stream == stream && def.Name == field {
			return def
		}
	}
	return model.FieldDef{Name: field, Stream: stream, Scope: streamScope}
}

// readMetricRows 读取一个 JSONL 流。文件不存在返回空切片（流可能因采集
// 失败而只有声明）；坏行跳过，不中断其余数据。
func readMetricRows(path string) ([]metricRow, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var rows []metricRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber()
		var vals map[string]any
		if err := dec.Decode(&vals); err != nil {
			continue
		}
		row := metricRow{vals: vals, order: len(rows)}
		if s, ok := vals["ts"].(string); ok {
			row.ts = s
			if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
				row.at = at
				row.hasTime = true
			}
		}
		rows = append(rows, row)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// 统计与首末值都依赖时间顺序；无 ts 的行保持文件内相对顺序。
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.hasTime && b.hasTime {
			return a.at.Before(b.at)
		}
		return a.order < b.order
	})
	return rows, nil
}

func writeSeries(path string, points []model.SeriesPoint) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, p := range points {
		if err := enc.Encode(p); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// numeric 把 JSON 数值转为 float64。字符串/布尔/对象都不算数值。
func numeric(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// resolveScope 解析一条记录所属的对象。
//
// 只对“按对象分维”的 scope 从行内取标识：cpu→cpu、disk/device→device、
// fs→mount、iface→iface。host/net 等流级 counter（如 ctx_switches）没有
// 行内对象，使用字段字典的 scope（缺省再退回流 scope），与样例包一致。
func resolveScope(def model.FieldDef, meta model.StreamMeta, row metricRow) string {
	key := ""
	switch def.Scope {
	case "iface":
		key = "iface"
	case "disk", "device":
		key = "device"
	case "fs":
		key = "mount"
	case "cpu":
		key = "cpu"
	}
	if key != "" {
		if s, ok := row.vals[key].(string); ok && s != "" {
			return s
		}
		if def.Scope == "fs" {
			if s, ok := row.vals["device"].(string); ok && s != "" {
				return s
			}
		}
	}
	if def.Scope != "" && def.Scope != "other" {
		return def.Scope
	}
	return meta.Scope
}

// streamIntervalS 返回流 start→end 的秒数；缺失或非正时用整包采样时长。
func streamIntervalS(meta model.StreamMeta, fallback time.Duration) float64 {
	start, err1 := time.Parse(time.RFC3339, meta.Start)
	end, err2 := time.Parse(time.RFC3339, meta.End)
	if err1 == nil && err2 == nil {
		if d := end.Sub(start).Seconds(); d > 0 {
			return d
		}
	}
	if fallback > 0 {
		return fallback.Seconds()
	}
	return 0
}
