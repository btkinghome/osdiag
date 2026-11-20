package derive

import (
	"math"
	"sort"
	"time"

	"luanbird/osdiag/internal/model"
)

// buildSeries 按 formulas.go 中的公式逐采样点计算 L1 序列。
// 无法计算（缺字段、分母为 0、计数器回退）的点直接跳过。
func buildSeries(streams []streamData, dict model.FieldDictionary) []model.SeriesPoint {
	points := []model.SeriesPoint{}
	for _, sd := range streams {
		switch sd.meta.ID {
		case "cpu":
			points = appendCPUSeries(points, sd, dict)
		case "memory":
			points = appendMemorySeries(points, sd, dict)
		case "network":
			points = appendNetworkSeries(points, sd, dict)
		}
	}
	sort.SliceStable(points, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339Nano, points[i].TS)
		tj, _ := time.Parse(time.RFC3339Nano, points[j].TS)
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		if points[i].Name != points[j].Name {
			return points[i].Name < points[j].Name
		}
		return points[i].Scope < points[j].Scope
	})
	return points
}

func appendCPUSeries(dst []model.SeriesPoint, sd streamData, dict model.FieldDictionary) []model.SeriesPoint {
	def := lookupDef(dict, sd.meta.ID, "idle_pct", sd.meta.Scope)
	for _, row := range sd.rows {
		if !row.hasTime {
			continue
		}
		idle, ok := numeric(row.vals["idle_pct"])
		if !ok {
			continue
		}
		dst = appendPoint(dst, row, "cpu.util_pct", 100-idle, "%", resolveScope(def, sd.meta, row))
	}
	return dst
}

func appendMemorySeries(dst []model.SeriesPoint, sd streamData, dict model.FieldDictionary) []model.SeriesPoint {
	availDef := lookupDef(dict, sd.meta.ID, "mem_available_bytes", sd.meta.Scope)
	for _, row := range sd.rows {
		if !row.hasTime {
			continue
		}
		avail, ok := numeric(row.vals["mem_available_bytes"])
		if !ok {
			continue
		}
		total, ok := numeric(row.vals["mem_total_bytes"])
		if !ok || total == 0 {
			continue
		}
		dst = appendPoint(dst, row, "memory.available_ratio", avail/total, "1", resolveScope(availDef, sd.meta, row))
	}
	return dst
}

// appendNetworkSeries 对每个 scope 分别维护上一个采样点，按相邻 ts 求增量。
// 计数器回退（delta<0）无法得到有效速率，跳过该点并把基线更新到新值。
func appendNetworkSeries(dst []model.SeriesPoint, sd streamData, dict model.FieldDictionary) []model.SeriesPoint {
	def := lookupDef(dict, sd.meta.ID, "rx_bytes", sd.meta.Scope)
	type prevPoint struct {
		value float64
		at    time.Time
		ok    bool
	}
	prev := map[string]prevPoint{}
	for _, row := range sd.rows {
		if !row.hasTime {
			continue
		}
		rx, ok := numeric(row.vals["rx_bytes"])
		if !ok {
			continue
		}
		scope := resolveScope(def, sd.meta, row)
		p := prev[scope]
		if p.ok {
			if dt := row.at.Sub(p.at).Seconds(); dt > 0 {
				if delta := rx - p.value; delta >= 0 {
					// 8 bit/Byte，1e6 为兆：bytes/s -> Mbps。
					dst = appendPoint(dst, row, "network.rx_mbps", delta*8/dt/1e6, "Mbps", scope)
				}
			}
		}
		prev[scope] = prevPoint{value: rx, at: row.at, ok: true}
	}
	return dst
}

func appendPoint(dst []model.SeriesPoint, row metricRow, name string, value float64, unit, scope string) []model.SeriesPoint {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return dst
	}
	return append(dst, model.SeriesPoint{
		TS:    row.ts,
		Name:  name,
		Value: value,
		Unit:  unit,
		Scope: scope,
	})
}
