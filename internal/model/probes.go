package model

// ProbeResult 对应 probes/<domain>.<name>.json。
type ProbeResult struct {
	SchemaVersion string         `json:"schema_version"`
	Probe         string         `json:"probe"`
	Level         string         `json:"level"`
	StartedAt     string         `json:"started_at"`
	DurationMS    float64        `json:"duration_ms"`
	OK            bool           `json:"ok"`
	Skipped       bool           `json:"skipped"`
	SkipReason    string         `json:"skip_reason,omitempty"`
	Error         *ProbeError    `json:"error,omitempty"`
	Metrics       map[string]any `json:"metrics,omitempty"`
	Samples       []ProbeSample  `json:"samples,omitempty"`
}

type ProbeError struct {
	Errno string `json:"errno"`
	Name  string `json:"name,omitempty"`
	Msg   string `json:"msg"`
}

type ProbeSample struct {
	TS         string `json:"ts"`
	Op         string `json:"op"`
	DurationUS float64 `json:"duration_us"`
	Bytes      *int64 `json:"bytes,omitempty"`
	Errno      string `json:"errno,omitempty"`
}

// RunlogEvent 对应 probes/runlog.jsonl 的每行。
type RunlogEvent struct {
	SchemaVersion string   `json:"schema_version,omitempty"`
	TS            string   `json:"ts"`
	Probe         string   `json:"probe"`
	Event         string   `json:"event"`
	Level         string   `json:"level,omitempty"`
	DurationMS    *float64 `json:"duration_ms,omitempty"`
	Detail        string   `json:"detail,omitempty"`
}
