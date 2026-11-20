// Package model 定义证据包 1.0 的产物结构。
// 字段与 docs/osdiag/schema/*.schema.json 一一对应。
package model

// SchemaVersion 是产物 schema 版本，冻结于设计稿 v0.3。
const SchemaVersion = "1.0"

type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Go      string `json:"go"`
	Build   string `json:"build,omitempty"`
	Commit  string `json:"commit,omitempty"`
}

type Run struct {
	ID         string   `json:"id"`
	Profile    string   `json:"profile"`
	Start      string   `json:"start"`
	End        string   `json:"end"`
	DurationS  int      `json:"duration_s"`
	Status     string   `json:"status"`
	ExitCode   int      `json:"exit_code"`
	Args       []string `json:"args"`
	ConfigHash string   `json:"config_hash"`
}

type OS struct {
	ID               string `json:"id,omitempty"`
	VersionID        string `json:"version_id,omitempty"`
	PrettyName       string `json:"pretty_name,omitempty"`
	KernelBaselineOK bool   `json:"kernel_baseline_ok"`
}

type Serials struct {
	System  string `json:"system,omitempty"`
	Board   string `json:"board,omitempty"`
	Product string `json:"product,omitempty"`
	Source  string `json:"source"`
}

type Host struct {
	Hostname      string   `json:"hostname"`
	FQDN          string   `json:"fqdn,omitempty"`
	Fingerprint   string   `json:"fingerprint,omitempty"`
	MachineIDHash string   `json:"machine_id_hash,omitempty"`
	BootID        string   `json:"boot_id,omitempty"`
	Kernel        string   `json:"kernel"`
	Arch          string   `json:"arch"`
	NCPU          int      `json:"ncpu"`
	MemBytes      uint64   `json:"mem_bytes"`
	Env           string   `json:"env"`
	Container     bool     `json:"container"`
	OS            OS       `json:"os"`
	Serials       *Serials `json:"serials,omitempty"`
}

type ProbePolicy struct {
	AllowHeavy           bool     `json:"allow_heavy"`
	AllowExternal        bool     `json:"allow_external"`
	Enabled              []string `json:"enabled"`
	SkippedDueToPressure []string `json:"skipped_due_to_pressure"`
}

type Gap struct {
	Stream   string `json:"stream"`
	Expected int    `json:"expected"`
	Actual   int    `json:"actual"`
	Reason   string `json:"reason"`
	Detail   string `json:"detail,omitempty"`
}

type Coverage struct {
	ExpectedSamples int     `json:"expected_samples"`
	ActualSamples   int     `json:"actual_samples"`
	Ratio           float64 `json:"ratio"`
	Gaps            []Gap   `json:"gaps"`
}

type Capability struct {
	ID       string `json:"id"`
	Present  bool   `json:"present"`
	Readable bool   `json:"readable"`
	Errno    string `json:"errno,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type Artifact struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Rows   *int   `json:"rows,omitempty"`
	Schema string `json:"schema,omitempty"`
}

type RunError struct {
	Collector string `json:"collector"`
	Msg       string `json:"msg"`
}

type Overhead struct {
	CPUMs      int64 `json:"cpu_ms"`
	RSSKB      int64 `json:"rss_kb"`
	ReadBytes  int64 `json:"read_bytes"`
	WriteBytes int64 `json:"write_bytes"`
}

type Marker struct {
	TS      string `json:"ts"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type NetTarget struct {
	Target    string   `json:"target"`
	Proto     string   `json:"proto"`
	Attempted bool     `json:"attempted"`
	OK        bool     `json:"ok"`
	RTTMs     *float64 `json:"rtt_ms,omitempty"`
}

type Counts struct {
	Processes        int `json:"processes,omitempty"`
	ListeningSockets int `json:"listening_sockets,omitempty"`
	KernelModules    int `json:"kernel_modules,omitempty"`
	PCIDevices       int `json:"pci_devices,omitempty"`
	BlockDevices     int `json:"block_devices,omitempty"`
}

// Manifest 对应 manifest.schema.json。
type Manifest struct {
	SchemaVersion string       `json:"schema_version"`
	Tool          Tool         `json:"tool"`
	Run           Run          `json:"run"`
	Host          Host         `json:"host"`
	ProbePolicy   ProbePolicy  `json:"probe_policy"`
	Coverage      Coverage     `json:"coverage"`
	Capabilities  []Capability `json:"capabilities"`
	Artifacts     []Artifact   `json:"artifacts"`
	Errors        []RunError   `json:"errors"`
	Overhead      Overhead     `json:"overhead"`
	Markers       []Marker     `json:"markers"`
	NetTargets    []NetTarget  `json:"net_targets"`
	Counts        *Counts      `json:"counts,omitempty"`
}

// Catalog 对应 catalog.schema.json。
type Catalog struct {
	SchemaVersion string         `json:"schema_version"`
	Entries       []CatalogEntry `json:"entries"`
}

type CatalogEntry struct {
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Priority    int    `json:"priority"`
	Description string `json:"description"`
	Bytes       int64  `json:"bytes"`
	Rows        *int   `json:"rows,omitempty"`
	Schema      string `json:"schema,omitempty"`
	Notes       string `json:"notes,omitempty"`
}
