package model

// StreamIndex 对应 metrics/streams.json。
type StreamIndex struct {
	SchemaVersion string       `json:"schema_version"`
	Streams       []StreamMeta `json:"streams"`
}

type StreamMeta struct {
	ID         string   `json:"id"`
	Path       string   `json:"path"`
	IntervalMS int      `json:"interval_ms"`
	Rows       int      `json:"rows"`
	Start      string   `json:"start"`
	End        string   `json:"end"`
	Scope      string   `json:"scope"`
	Fields     []string `json:"fields"`
	Quality    []string `json:"quality"`
	Notes      string   `json:"notes,omitempty"`
}

// FieldDictionary 对应 agent/field-dictionary.json。
type FieldDictionary struct {
	SchemaVersion string     `json:"schema_version"`
	Fields        []FieldDef `json:"fields"`
}

type FieldDef struct {
	Name        string `json:"name"`
	Stream      string `json:"stream"`
	Type        string `json:"type"`
	Unit        string `json:"unit,omitempty"`
	Kind        string `json:"kind"`
	Scope       string `json:"scope"`
	Description string `json:"description"`
	Formula     string `json:"formula,omitempty"`
}

// FieldStatsDoc 对应 derived/field-stats.json。
type FieldStatsDoc struct {
	SchemaVersion string      `json:"schema_version"`
	GeneratedAt   string      `json:"generated_at"`
	Streams       []FieldStat `json:"streams"`
}

type FieldStat struct {
	Stream  string   `json:"stream"`
	Field   string   `json:"field"`
	Count   int      `json:"count"`
	Nulls   int      `json:"nulls"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Mean    *float64 `json:"mean,omitempty"`
	P50     *float64 `json:"p50,omitempty"`
	P95     *float64 `json:"p95,omitempty"`
	P99     *float64 `json:"p99,omitempty"`
	Quality []string `json:"quality,omitempty"`
}

// FormulasDoc 对应 derived/formulas.json。
type FormulasDoc struct {
	SchemaVersion string    `json:"schema_version"`
	Formulas      []Formula `json:"formulas"`
}

type Formula struct {
	Name        string   `json:"name"`
	Formula     string   `json:"formula"`
	Unit        string   `json:"unit"`
	Inputs      []string `json:"inputs"`
	Description string   `json:"description"`
}

// DeltasDoc 对应 derived/deltas.json。
type DeltasDoc struct {
	SchemaVersion string  `json:"schema_version"`
	IntervalS     float64 `json:"interval_s"`
	Fields        []Delta `json:"fields"`
}

type Delta struct {
	Stream       string   `json:"stream"`
	Field        string   `json:"field"`
	Scope        string   `json:"scope,omitempty"`
	Start        float64  `json:"start"`
	End          float64  `json:"end"`
	Delta        *float64 `json:"delta,omitempty"`
	RatePerS     *float64 `json:"rate_per_s,omitempty"`
	CounterReset bool     `json:"counter_reset"`
}

// SeriesPoint 对应 derived/series.jsonl 的每行。
type SeriesPoint struct {
	TS      string  `json:"ts"`
	Name    string  `json:"name"`
	Value   float64 `json:"value"`
	Unit    string  `json:"unit"`
	Scope   string  `json:"scope,omitempty"`
	Quality string  `json:"quality,omitempty"`
}
