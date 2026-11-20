package model

// LogsIndex 对应 logs/index.json。
type LogsIndex struct {
	SchemaVersion string       `json:"schema_version"`
	Files         []LogFile    `json:"files"`
	Patterns      []LogPattern `json:"patterns"`
	Journal       *JournalInfo `json:"journal,omitempty"`
}

type LogFile struct {
	Path      string `json:"path"`
	Source    string `json:"source"`
	Bytes     int64  `json:"bytes"`
	Lines     *int   `json:"lines,omitempty"`
	Truncated bool   `json:"truncated"`
	Rotated   *bool  `json:"rotated,omitempty"`
	Binary    *bool  `json:"binary,omitempty"`
	FirstTS   string `json:"first_ts,omitempty"`
	LastTS    string `json:"last_ts,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type LogPattern struct {
	Category string         `json:"category"`
	Count    int            `json:"count"`
	FirstTS  string         `json:"first_ts,omitempty"`
	LastTS   string         `json:"last_ts,omitempty"`
	Samples  []PatternEntry `json:"samples,omitempty"`
}

type PatternEntry struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type JournalInfo struct {
	Present  bool   `json:"present"`
	Parsed   bool   `json:"parsed"`
	Bytes    *int64 `json:"bytes,omitempty"`
	Files    *int   `json:"files,omitempty"`
	Earliest string `json:"earliest,omitempty"`
	Latest   string `json:"latest,omitempty"`
	Gap      string `json:"gap,omitempty"`
}
