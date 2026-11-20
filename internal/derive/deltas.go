package derive

import (
	"sort"
	"time"

	"luanbird/osdiag/internal/model"
)

// buildDeltas 对 kind=counter 的字段按 (stream, field, scope) 取首末值。
// end<start 视为 counter 重置：只置 counter_reset=true，不输出 delta/rate。
func buildDeltas(streams []streamData, dict model.FieldDictionary, fallback time.Duration) model.DeltasDoc {
	doc := model.DeltasDoc{
		SchemaVersion: model.SchemaVersion,
		Fields:        []model.Delta{},
	}
	for _, sd := range streams {
		interval := streamIntervalS(sd.meta, fallback)
		if doc.IntervalS == 0 && interval > 0 {
			doc.IntervalS = interval
		}
		for _, def := range dict.Fields {
			if def.Stream != sd.meta.ID || def.Kind != "counter" {
				continue
			}

			type sample struct{ v float64 }
			groups := map[string][]sample{}
			var scopes []string
			for _, row := range sd.rows {
				f, ok := numeric(row.vals[def.Name])
				if !ok {
					continue
				}
				scope := resolveScope(def, sd.meta, row)
				if _, seen := groups[scope]; !seen {
					scopes = append(scopes, scope)
				}
				groups[scope] = append(groups[scope], sample{v: f})
			}
			// 行已按 ts 排序，因此每组首末即时间上的首末。
			sort.Strings(scopes)
			for _, scope := range scopes {
				g := groups[scope]
				if len(g) == 0 {
					continue
				}
				start, end := g[0].v, g[len(g)-1].v
				d := model.Delta{
					Stream: sd.meta.ID,
					Field:  def.Name,
					Scope:  scope,
					Start:  start,
					End:    end,
				}
				if end < start {
					d.CounterReset = true
				} else {
					delta := end - start
					d.Delta = floatPtr(delta)
					if interval > 0 {
						rate := delta / interval
						d.RatePerS = floatPtr(rate)
					}
				}
				doc.Fields = append(doc.Fields, d)
			}
		}
	}
	if doc.IntervalS == 0 && fallback > 0 {
		doc.IntervalS = fallback.Seconds()
	}
	return doc
}
