package model

// ProcessRecord 对应 top/processes.jsonl 的每行。
type ProcessRecord struct {
	TS         string      `json:"ts"`
	Phase      string      `json:"phase"`
	PID        int         `json:"pid"`
	PPID       *int        `json:"ppid,omitempty"`
	PGID       *int        `json:"pgid,omitempty"`
	SID        *int        `json:"sid,omitempty"`
	State      string      `json:"state"`
	Comm       string      `json:"comm"`
	Cmdline    []string    `json:"cmdline,omitempty"`
	Exe        string      `json:"exe,omitempty"`
	Cwd        string      `json:"cwd,omitempty"`
	Root       string      `json:"root,omitempty"`
	UID        *int        `json:"uid,omitempty"`
	GID        *int        `json:"gid,omitempty"`
	StartTicks *uint64     `json:"start_ticks,omitempty"`
	UtimeTicks *uint64     `json:"utime_ticks,omitempty"`
	StimeTicks *uint64     `json:"stime_ticks,omitempty"`
	RSSBytes   *uint64     `json:"rss_bytes,omitempty"`
	VSZBytes   *uint64     `json:"vsz_bytes,omitempty"`
	Threads    *int        `json:"threads,omitempty"`
	IORead     *uint64     `json:"io_read_bytes,omitempty"`
	IOWrite    *uint64     `json:"io_write_bytes,omitempty"`
	DelayBlkio *uint64     `json:"delayacct_blkio_ticks,omitempty"`
	OOMScore   *int        `json:"oom_score,omitempty"`
	OOMAdj     *int        `json:"oom_score_adj,omitempty"`
	Cgroup     string      `json:"cgroup,omitempty"`
	Unit       string      `json:"unit,omitempty"`
	NS         *Namespaces `json:"ns,omitempty"`
	Sockets    []SocketRef `json:"sockets,omitempty"`
	Environ    []string    `json:"environ,omitempty"`
}

type Namespaces struct {
	PID  *uint64 `json:"pid,omitempty"`
	Mnt  *uint64 `json:"mnt,omitempty"`
	Net  *uint64 `json:"net,omitempty"`
	User *uint64 `json:"user,omitempty"`
}

type SocketRef struct {
	Proto  string `json:"proto"`
	Local  string `json:"local"`
	Remote string `json:"remote,omitempty"`
	State  string `json:"state"`
	Inode  *int64 `json:"inode,omitempty"`
}

// TopSummary 对应 top/summary.json。
type TopSummary struct {
	SchemaVersion string       `json:"schema_version"`
	GeneratedAt   string       `json:"generated_at"`
	Counts        TopCounts    `json:"counts"`
	TopCPU        []TopProcCPU `json:"top_cpu,omitempty"`
	TopRSS        []TopProcRSS `json:"top_rss,omitempty"`
	TopIO         []TopProcIO  `json:"top_io,omitempty"`
	DState        []DStateProc `json:"dstate,omitempty"`
}

type TopCounts struct {
	Processes        int `json:"processes"`
	Threads          int `json:"threads"`
	KernelThreads    int `json:"kernel_threads"`
	Zombies          int `json:"zombies"`
	DState           int `json:"dstate"`
	ListeningSockets int `json:"listening_sockets"`
}

type TopProcCPU struct {
	PID     int     `json:"pid"`
	Comm    string  `json:"comm"`
	Cmdline string  `json:"cmdline,omitempty"`
	CPUMs   float64 `json:"cpu_ms"`
}

type TopProcRSS struct {
	PID      int    `json:"pid"`
	Comm     string `json:"comm"`
	Cmdline  string `json:"cmdline,omitempty"`
	RSSBytes uint64 `json:"rss_bytes"`
}

type TopProcIO struct {
	PID        int    `json:"pid"`
	Comm       string `json:"comm"`
	Cmdline    string `json:"cmdline,omitempty"`
	ReadBytes  uint64 `json:"read_bytes"`
	WriteBytes uint64 `json:"write_bytes"`
}

type DStateProc struct {
	PID        int    `json:"pid"`
	Comm       string `json:"comm"`
	State      string `json:"state"`
	Cmdline    string `json:"cmdline,omitempty"`
	Wchan      string `json:"wchan,omitempty"`
	DelayBlkio uint64 `json:"delayacct_blkio_ticks,omitempty"`
}
