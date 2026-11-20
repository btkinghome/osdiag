package derive

import (
	"math"
	"sort"
	"time"

	"luanbird/osdiag/internal/model"
)

// buildFieldStats 对字段字典声明为 number/integer 的字段做分布统计。
// 每个 (stream, field) 的 count 只统计非 null 数值，nulls 统计显式 null
// 或缺失字段。
func buildFieldStats(streams []streamData, dict model.FieldDictionary) model.FieldStatsDoc {
	doc := model.FieldStatsDoc{
		SchemaVersion: model.SchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Streams:       []model.FieldStat{},
	}
	for _, sd := range streams {
		for _, def := range dict.Fields {
			if def.Stream != sd.meta.ID {
				continue
			}
			if def.Type != "number" && def.Type != "integer" {
				continue
			}
			var vals []float64
			nulls := 0
			for _, row := range sd.rows {
				v, ok := row.vals[def.Name]
				if !ok || v == nil {
					nulls++
					continue
				}
				if f, ok := numeric(v); ok {
					vals = append(vals, f)
				}
			}
			if len(vals) == 0 && nulls == 0 {
				// 字段在实际数据中完全没出现：不制造噪声。
				continue
			}
			sort.Float64s(vals)

			st := model.FieldStat{
				Stream: sd.meta.ID,
				Field:  def.Name,
				Count:  len(vals),
				Nulls:  nulls,
			}
			// 继承流 quality；只保留 field-stats schema 枚举允许的值，
			// 避免 topology_change/denied 等流级标记让产物过不了校验。
			st.Quality = fieldStatQuality(sd.meta.Quality)
			if len(vals) > 0 {
				sum := 0.0
				for _, v := range vals {
					sum += v
				}
				st.Min = floatPtr(vals[0])
				st.Max = floatPtr(vals[len(vals)-1])
				st.Mean = floatPtr(sum / float64(len(vals)))
				st.P50 = floatPtr(percentile(vals, 0.50))
				st.P95 = floatPtr(percentile(vals, 0.95))
				st.P99 = floatPtr(percentile(vals, 0.99))
			}
			doc.Streams = append(doc.Streams, st)
		}
	}
	return doc
}

// percentile 使用 nearest-rank：对升序样本取 ceil(q*n) 名（1-based），
// 不做线性插值。q=0.95、n=3 时取第 3 个样本。
func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(q * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// fieldStatQuality 过滤为 field-stats.schema.json 的 quality 枚举。
func fieldStatQuality(quality []string) []string {
	allowed := map[string]bool{
		"ok": true, "gap": true, "counter_reset": true,
		"clock_step": true, "truncated": true, "partial": true,
	}
	out := make([]string, 0, len(quality))
	for _, q := range quality {
		if allowed[q] {
			out = append(out, q)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func floatPtr(v float64) *float64 { return &v }
