// Package metrics 采集 M1 指标流：cpu/memory/pressure/disk/filesystem/network/conntrack。
//
// 每条流独立 goroutine 采样并写入 metrics/<id>.jsonl（逐行 JSON），最后汇总为
// metrics/streams.json 与 agent/field-dictionary.json。采样错误只通过
// env.AddError 上报，不中断整体 run。
package metrics

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
	"luanbird/osdiag/internal/pack"
)

const collectorName = "metrics"

// errSkipStream 表示流在当前内核上不存在（例如未启用 conntrack），
// 此时既不登记流也不写文件。
var errSkipStream = errors.New("stream unavailable")

// unsupportedError 表示流需要的内核能力缺失，应登记为空流并标记 unsupported。
type unsupportedError struct{ note string }

func (e *unsupportedError) Error() string { return e.note }

type row map[string]any

type collector interface {
	// setup 做一次发现，返回每个采样点的实体数（设备/挂载点/接口数）。
	setup() (int, error)
	sample(now time.Time) ([]row, error)
}

type streamDef struct {
	id           string
	scope        string
	fields       []string
	newCollector func(interval time.Duration) collector
}

var cpuFields = []string{
	"cpu", "user_pct", "system_pct", "iowait_pct", "idle_pct", "steal_pct",
	"load1", "load5", "load15", "procs_running", "procs_blocked", "ctx_switches",
}

var memoryFields = []string{
	"mem_total_bytes", "mem_available_bytes", "mem_free_bytes", "buffers_bytes",
	"cached_bytes", "swap_total_bytes", "swap_free_bytes", "dirty_bytes",
	"writeback_bytes", "anon_bytes", "slab_bytes", "hugepages_total", "hugepages_free",
}

var pressureFields = []string{
	"some_cpu_avg10", "some_cpu_avg60", "some_cpu_avg300",
	"some_io_avg10", "some_io_avg60", "some_io_avg300",
	"some_memory_avg10", "some_memory_avg60", "some_memory_avg300",
	"full_io_avg10", "full_io_avg60", "full_io_avg300",
}

var diskFields = []string{
	"device", "read_iops", "write_iops", "read_bytes_per_s", "write_bytes_per_s",
	"write_bytes_total", "await_ms", "util_pct", "inflight",
}

var filesystemFields = []string{
	"mount", "device", "fstype", "size_bytes", "used_bytes", "avail_bytes",
	"inodes_total", "inodes_free", "read_only",
}

var networkFields = []string{
	"iface", "rx_bytes", "tx_bytes", "rx_packets", "tx_packets",
	"rx_errors", "tx_errors", "rx_dropped", "tx_dropped",
	"speed_mbps", "carrier", "operstate",
}

var conntrackFields = []string{
	"entries", "max", "insert_failed", "drop", "early_drop", "search_restart",
}

func streamDefinitions() []*streamDef {
	return []*streamDef{
		{
			id: "cpu", scope: "cpu", fields: cpuFields,
			newCollector: func(time.Duration) collector { return &cpuCollector{} },
		},
		{
			id: "memory", scope: "host", fields: memoryFields,
			newCollector: func(time.Duration) collector { return &memoryCollector{} },
		},
		{
			id: "pressure", scope: "host", fields: pressureFields,
			newCollector: func(time.Duration) collector { return &pressureCollector{} },
		},
		{
			id: "disk", scope: "disk", fields: diskFields,
			newCollector: func(interval time.Duration) collector {
				return &diskCollector{interval: interval}
			},
		},
		{
			id: "filesystem", scope: "fs", fields: filesystemFields,
			newCollector: func(time.Duration) collector { return &fsCollector{} },
		},
		{
			id: "network", scope: "iface", fields: networkFields,
			newCollector: func(time.Duration) collector { return &netCollector{} },
		},
		{
			id: "conntrack", scope: "net", fields: conntrackFields,
			newCollector: func(time.Duration) collector { return &conntrackCollector{} },
		},
	}
}

type streamResult struct {
	id     string
	path   string
	scope  string
	fields []string

	rows        int
	firstTS     string
	lastTS      string
	start       string
	end         string
	note        string
	skipped     bool
	unsupported bool
	setupErr    bool

	expectedTicks int
	maxEntities   int
	skippedTicks  int
	shortTicks    int
	canceled      bool
	errs          []string
}

func (r *streamResult) expected() int {
	if r.expectedTicks <= 0 || r.maxEntities <= 0 {
		return 0
	}
	return r.expectedTicks * r.maxEntities
}

func (r *streamResult) addErr(err error) {
	if err == nil {
		return
	}
	msg := truncateRunes(err.Error(), 300)
	for _, e := range r.errs {
		if e == msg {
			return
		}
	}
	if len(r.errs) < 6 {
		r.errs = append(r.errs, msg)
	}
}

func (r *streamResult) quality() []string {
	if r.unsupported {
		return []string{"unsupported"}
	}
	if r.skipped {
		return nil
	}
	q := make([]string, 0, 3)
	add := func(s string) {
		for _, x := range q {
			if x == s {
				return
			}
		}
		q = append(q, s)
	}
	if r.rows > 0 {
		add("ok")
	}
	if r.rows == 0 || r.canceled || r.skippedTicks > 0 || r.shortTicks > 0 || r.rows < r.expected() {
		add("gap")
	}
	if len(r.errs) > 0 && r.rows > 0 {
		add("partial")
	}
	if len(q) == 0 {
		add("gap")
	}
	return q
}

func (r *streamResult) issueSummary() string {
	if r.setupErr && len(r.errs) > 0 {
		return "流初始化失败: " + r.errs[0]
	}
	parts := []string{fmt.Sprintf("期望 %d 行，实际 %d 行", r.expected(), r.rows)}
	if r.canceled {
		parts = append(parts, "上下文取消提前停止")
	}
	if r.skippedTicks > 0 {
		parts = append(parts, fmt.Sprintf("调度落后跳过 %d 个时间点", r.skippedTicks))
	}
	if r.shortTicks > 0 {
		parts = append(parts, fmt.Sprintf("%d 个时间点有实体读取失败", r.shortTicks))
	}
	if len(r.errs) > 0 {
		parts = append(parts, "部分采样读取失败")
	}
	return joinText(parts)
}

func (r *streamResult) notes() string {
	if r.note != "" {
		return truncateRunes(r.note, 500)
	}
	if r.rows == 0 {
		return "未产生采样行"
	}
	if r.rows < r.expected() || r.canceled || r.skippedTicks > 0 || r.shortTicks > 0 || len(r.errs) > 0 {
		return truncateRunes(r.issueSummary(), 500)
	}
	return ""
}

func joinText(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "；"
		}
		out += p
	}
	return out
}

func truncateRunes(s string, limit int) string {
	rs := []rune(s)
	if len(rs) <= limit {
		return s
	}
	return string(rs[:limit])
}

// Run 执行 7 条 M1 指标流的采集窗口。返回 nil 表示编排本身完成；
// 所有采集错误都记录到 env，不使 run 失败。
func Run(ctx context.Context, env *collect.Env) error {
	if env == nil {
		return nil
	}
	interval := time.Second
	if env.Profile == "deep" {
		interval = 2 * time.Second
	}
	start := env.Start
	if start.IsZero() {
		start = time.Now().UTC()
	}
	deadline := start.Add(env.Duration)
	windowStart := start.UTC().Format(time.RFC3339)
	windowEnd := deadline.UTC().Format(time.RFC3339)

	if err := os.MkdirAll(env.Path("metrics"), 0o755); err != nil {
		env.AddError(collectorName, "创建 metrics 目录失败: %v", err)
		return nil
	}

	defs := streamDefinitions()
	results := make([]*streamResult, len(defs))
	var wg sync.WaitGroup
	for i, def := range defs {
		wg.Add(1)
		go func(i int, def *streamDef) {
			defer wg.Done()
			results[i] = runStream(ctx, env, def, interval, start, deadline, windowStart, windowEnd)
		}(i, def)
	}
	wg.Wait()

	index := model.StreamIndex{SchemaVersion: model.SchemaVersion, Streams: []model.StreamMeta{}}
	registered := make(map[string]bool, len(defs))
	cov := model.Coverage{Gaps: []model.Gap{}}

	for _, res := range results {
		if res == nil || res.skipped {
			continue
		}
		registered[res.id] = true
		index.Streams = append(index.Streams, model.StreamMeta{
			ID:         res.id,
			Path:       res.path,
			IntervalMS: int(interval / time.Millisecond),
			Rows:       res.rows,
			Start:      res.start,
			End:        res.end,
			Scope:      res.scope,
			Fields:     res.fields,
			Quality:    res.quality(),
			Notes:      res.notes(),
		})

		if res.unsupported {
			cov.Gaps = append(cov.Gaps, model.Gap{
				Stream: res.id,
				Reason: "unsupported",
				Detail: truncateRunes(res.note, 500),
			})
			continue
		}

		exp := res.expected()
		cov.ExpectedSamples += exp
		cov.ActualSamples += res.rows
		if res.rows < exp || len(res.errs) > 0 {
			reason := "other"
			if res.setupErr || (res.rows == 0 && len(res.errs) > 0) {
				reason = "collector_error"
			}
			cov.Gaps = append(cov.Gaps, model.Gap{
				Stream:   res.id,
				Expected: exp,
				Actual:   res.rows,
				Reason:   reason,
				Detail:   truncateRunes(res.issueSummary(), 500),
			})
		}
	}
	for _, res := range results {
		if res == nil {
			continue
		}
		if res.skipped {
			cov.Gaps = append(cov.Gaps, model.Gap{
				Stream: res.id,
				Reason: "unsupported",
				Detail: truncateRunes(res.note, 500),
			})
		}
		for _, msg := range res.errs {
			env.AddError(collectorName, "流 %s: %s", res.id, msg)
		}
	}

	cov.Ratio = 1.0
	if cov.ExpectedSamples > 0 {
		r := float64(cov.ActualSamples) / float64(cov.ExpectedSamples)
		if r < 0 {
			r = 0
		}
		if r > 1 {
			r = 1
		}
		cov.Ratio = roundTo(r, 4)
	}
	env.SetCoverage(cov)

	if err := pack.WriteJSON(env.Path("metrics/streams.json"), index); err != nil {
		env.AddError(collectorName, "写入 metrics/streams.json 失败: %v", err)
	}
	dict := model.FieldDictionary{SchemaVersion: model.SchemaVersion, Fields: []model.FieldDef{}}
	for _, fd := range fieldDictionary {
		if registered[fd.Stream] {
			dict.Fields = append(dict.Fields, fd)
		}
	}
	if err := pack.WriteJSON(env.Path("agent/field-dictionary.json"), dict); err != nil {
		env.AddError(collectorName, "写入 agent/field-dictionary.json 失败: %v", err)
	}
	return nil
}

func runStream(ctx context.Context, env *collect.Env, def *streamDef, interval time.Duration,
	start, deadline time.Time, windowStart, windowEnd string) *streamResult {
	res := &streamResult{
		id:            def.id,
		path:          "metrics/" + def.id + ".jsonl",
		scope:         def.scope,
		fields:        def.fields,
		expectedTicks: int(deadline.Sub(start)/interval) + 1,
	}
	if res.expectedTicks < 1 {
		res.expectedTicks = 1
	}

	col := def.newCollector(interval)
	n, err := col.setup()
	res.maxEntities = n
	if err != nil {
		if errors.Is(err, errSkipStream) {
			res.skipped = true
			res.note = "内核未提供 nf_conntrack（连接跟踪未启用或模块未加载），跳过该流。"
			return res
		}
		var ue *unsupportedError
		if errors.As(err, &ue) {
			res.unsupported = true
			res.note = ue.note
			res.start, res.end = windowStart, windowEnd
			if werr := writeEmptyFile(env.Path(res.path)); werr != nil {
				res.addErr(fmt.Errorf("创建空流文件失败: %w", werr))
			}
			return res
		}
		if errors.Is(err, fs.ErrNotExist) {
			res.unsupported = true
			res.note = fmt.Sprintf("依赖路径不存在: %v", err)
			res.start, res.end = windowStart, windowEnd
			if werr := writeEmptyFile(env.Path(res.path)); werr != nil {
				res.addErr(fmt.Errorf("创建空流文件失败: %w", werr))
			}
			return res
		}
		res.setupErr = true
		res.addErr(err)
		res.start, res.end = windowStart, windowEnd
		if werr := writeEmptyFile(env.Path(res.path)); werr != nil {
			res.addErr(fmt.Errorf("创建空流文件失败: %w", werr))
		}
		return res
	}

	if err := os.MkdirAll(filepath.Dir(env.Path(res.path)), 0o755); err != nil {
		res.addErr(err)
		res.start, res.end = windowStart, windowEnd
		return res
	}
	f, err := os.Create(env.Path(res.path))
	if err != nil {
		res.addErr(err)
		res.start, res.end = windowStart, windowEnd
		return res
	}
	bw := bufio.NewWriterSize(f, 64*1024)

	writeFailed := false
	for i := 0; i < res.expectedTicks; i++ {
		at := start.Add(time.Duration(i) * interval)
		if !sleepUntil(ctx, at) {
			res.canceled = true
			break
		}
		now := time.Now()
		rows, serr := col.sample(now)
		if serr != nil {
			res.addErr(serr)
		}
		ts := now.UTC().Format(time.RFC3339)
		for _, r := range rows {
			r["ts"] = ts
			b, merr := json.Marshal(r)
			if merr != nil {
				res.addErr(fmt.Errorf("序列化 %s 行失败: %w", def.id, merr))
				continue
			}
			if _, werr := bw.Write(b); werr != nil {
				res.addErr(fmt.Errorf("写入 %s 失败: %w", res.path, werr))
				writeFailed = true
				break
			}
			if werr := bw.WriteByte('\n'); werr != nil {
				res.addErr(fmt.Errorf("写入 %s 失败: %w", res.path, werr))
				writeFailed = true
				break
			}
			res.rows++
			if res.firstTS == "" {
				res.firstTS = ts
			}
			res.lastTS = ts
		}
		if writeFailed {
			break
		}
		if len(rows) > res.maxEntities {
			res.maxEntities = len(rows)
		}
		if len(rows) < res.maxEntities {
			res.shortTicks++
		}
		// 采样耗时超过一个槽位时跳过已经错过的整点，避免补采样风暴。
		next := i + 1
		for next < res.expectedTicks {
			slot := start.Add(time.Duration(next) * interval)
			if !time.Now().After(slot.Add(interval / 2)) {
				break
			}
			res.skippedTicks++
			next++
		}
		i = next - 1
	}

	if err := bw.Flush(); err != nil {
		res.addErr(fmt.Errorf("刷新 %s 失败: %w", res.path, err))
	}
	if err := f.Close(); err != nil {
		res.addErr(fmt.Errorf("关闭 %s 失败: %w", res.path, err))
	}

	res.start, res.end = res.firstTS, res.lastTS
	if res.rows == 0 {
		res.start, res.end = windowStart, windowEnd
	}
	return res
}

// writeEmptyFile 创建真正的 0 字节流文件（pack.WriteText 会补一个换行）。
func writeEmptyFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, nil, 0o644)
}

func sleepUntil(ctx context.Context, at time.Time) bool {
	d := time.Until(at)
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// fieldDictionary 覆盖全部 7 条流的字段；写入时按实际登记的流过滤。
// scope 与所属流一致，kind 限定为 counter/gauge/bytes/enum/string。
var fieldDictionary = []model.FieldDef{
	// cpu
	{Name: "cpu", Stream: "cpu", Type: "string", Kind: "enum", Scope: "cpu", Description: "CPU 聚合标识，固定为 all（汇总全部逻辑 CPU）"},
	{Name: "user_pct", Stream: "cpu", Type: "number", Unit: "%", Kind: "gauge", Scope: "cpu", Description: "用户态 CPU 时间占比，包含 nice"},
	{Name: "system_pct", Stream: "cpu", Type: "number", Unit: "%", Kind: "gauge", Scope: "cpu", Description: "内核态 CPU 时间占比，包含硬中断与软中断"},
	{Name: "iowait_pct", Stream: "cpu", Type: "number", Unit: "%", Kind: "gauge", Scope: "cpu", Description: "等待块设备 IO 完成的 CPU 时间占比"},
	{Name: "idle_pct", Stream: "cpu", Type: "number", Unit: "%", Kind: "gauge", Scope: "cpu", Description: "空闲 CPU 时间占比"},
	{Name: "steal_pct", Stream: "cpu", Type: "number", Unit: "%", Kind: "gauge", Scope: "cpu", Description: "被虚拟化层抢占的 CPU 时间占比"},
	{Name: "load1", Stream: "cpu", Type: "number", Kind: "gauge", Scope: "cpu", Description: "最近 1 分钟的平均负载"},
	{Name: "load5", Stream: "cpu", Type: "number", Kind: "gauge", Scope: "cpu", Description: "最近 5 分钟的平均负载"},
	{Name: "load15", Stream: "cpu", Type: "number", Kind: "gauge", Scope: "cpu", Description: "最近 15 分钟的平均负载"},
	{Name: "procs_running", Stream: "cpu", Type: "integer", Kind: "gauge", Scope: "cpu", Description: "当前处于运行队列的进程数"},
	{Name: "procs_blocked", Stream: "cpu", Type: "integer", Kind: "gauge", Scope: "cpu", Description: "当前因 IO 阻塞的进程数"},
	{Name: "ctx_switches", Stream: "cpu", Type: "integer", Kind: "counter", Scope: "cpu", Description: "系统启动以来的上下文切换累计次数"},

	// memory
	{Name: "mem_total_bytes", Stream: "memory", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "host", Description: "物理内存总量"},
	{Name: "mem_available_bytes", Stream: "memory", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "host", Description: "可供新进程使用的内存估算值（MemAvailable）"},
	{Name: "mem_free_bytes", Stream: "memory", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "host", Description: "完全空闲的物理内存"},
	{Name: "buffers_bytes", Stream: "memory", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "host", Description: "块设备缓冲区占用的内存"},
	{Name: "cached_bytes", Stream: "memory", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "host", Description: "页缓存占用的内存（Cached）"},
	{Name: "swap_total_bytes", Stream: "memory", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "host", Description: "交换分区总大小"},
	{Name: "swap_free_bytes", Stream: "memory", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "host", Description: "交换分区剩余大小"},
	{Name: "dirty_bytes", Stream: "memory", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "host", Description: "等待写回磁盘的脏页大小"},
	{Name: "writeback_bytes", Stream: "memory", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "host", Description: "正在写回磁盘的页大小"},
	{Name: "anon_bytes", Stream: "memory", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "host", Description: "匿名页（AnonPages）占用大小"},
	{Name: "slab_bytes", Stream: "memory", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "host", Description: "内核 slab 占用大小"},
	{Name: "hugepages_total", Stream: "memory", Type: "integer", Kind: "gauge", Scope: "host", Description: "预分配的大页总数"},
	{Name: "hugepages_free", Stream: "memory", Type: "integer", Kind: "gauge", Scope: "host", Description: "空闲的大页数量"},

	// pressure
	{Name: "some_cpu_avg10", Stream: "pressure", Type: "number", Unit: "%", Kind: "gauge", Scope: "host", Description: "至少一个任务因 CPU 争用停顿的时间占比（10 秒均值）"},
	{Name: "some_cpu_avg60", Stream: "pressure", Type: "number", Unit: "%", Kind: "gauge", Scope: "host", Description: "至少一个任务因 CPU 争用停顿的时间占比（60 秒均值）"},
	{Name: "some_cpu_avg300", Stream: "pressure", Type: "number", Unit: "%", Kind: "gauge", Scope: "host", Description: "至少一个任务因 CPU 争用停顿的时间占比（300 秒均值）"},
	{Name: "some_io_avg10", Stream: "pressure", Type: "number", Unit: "%", Kind: "gauge", Scope: "host", Description: "至少一个任务因 IO 争用停顿的时间占比（10 秒均值）"},
	{Name: "some_io_avg60", Stream: "pressure", Type: "number", Unit: "%", Kind: "gauge", Scope: "host", Description: "至少一个任务因 IO 争用停顿的时间占比（60 秒均值）"},
	{Name: "some_io_avg300", Stream: "pressure", Type: "number", Unit: "%", Kind: "gauge", Scope: "host", Description: "至少一个任务因 IO 争用停顿的时间占比（300 秒均值）"},
	{Name: "some_memory_avg10", Stream: "pressure", Type: "number", Unit: "%", Kind: "gauge", Scope: "host", Description: "至少一个任务因内存争用停顿的时间占比（10 秒均值）"},
	{Name: "some_memory_avg60", Stream: "pressure", Type: "number", Unit: "%", Kind: "gauge", Scope: "host", Description: "至少一个任务因内存争用停顿的时间占比（60 秒均值）"},
	{Name: "some_memory_avg300", Stream: "pressure", Type: "number", Unit: "%", Kind: "gauge", Scope: "host", Description: "至少一个任务因内存争用停顿的时间占比（300 秒均值）"},
	{Name: "full_io_avg10", Stream: "pressure", Type: "number", Unit: "%", Kind: "gauge", Scope: "host", Description: "所有任务都因 IO 争用停顿的时间占比（10 秒均值）"},
	{Name: "full_io_avg60", Stream: "pressure", Type: "number", Unit: "%", Kind: "gauge", Scope: "host", Description: "所有任务都因 IO 争用停顿的时间占比（60 秒均值）"},
	{Name: "full_io_avg300", Stream: "pressure", Type: "number", Unit: "%", Kind: "gauge", Scope: "host", Description: "所有任务都因 IO 争用停顿的时间占比（300 秒均值）"},

	// disk
	{Name: "device", Stream: "disk", Type: "string", Kind: "string", Scope: "disk", Description: "块设备名（/sys/block 下的设备，如 sda、nvme0n1）"},
	{Name: "read_iops", Stream: "disk", Type: "number", Unit: "次/s", Kind: "gauge", Scope: "disk", Description: "每秒读操作次数，由累计计数器差分得到"},
	{Name: "write_iops", Stream: "disk", Type: "number", Unit: "次/s", Kind: "gauge", Scope: "disk", Description: "每秒写操作次数，由累计计数器差分得到"},
	{Name: "read_bytes_per_s", Stream: "disk", Type: "number", Unit: "B/s", Kind: "gauge", Scope: "disk", Description: "每秒读取字节数（扇区数乘以 512）"},
	{Name: "write_bytes_per_s", Stream: "disk", Type: "number", Unit: "B/s", Kind: "gauge", Scope: "disk", Description: "每秒写入字节数（扇区数乘以 512）"},
	{Name: "write_bytes_total", Stream: "disk", Type: "integer", Unit: "bytes", Kind: "counter", Scope: "disk", Description: "设备启动以来累计写入字节数"},
	{Name: "await_ms", Stream: "disk", Type: "number", Unit: "ms", Kind: "gauge", Scope: "disk", Description: "平均每次 IO 的等待加服务时间"},
	{Name: "util_pct", Stream: "disk", Type: "number", Unit: "%", Kind: "gauge", Scope: "disk", Description: "设备繁忙时间占比，按 io_ticks 差分计算，上限 100"},
	{Name: "inflight", Stream: "disk", Type: "integer", Kind: "gauge", Scope: "disk", Description: "当前正在处理的 IO 请求数"},

	// filesystem
	{Name: "mount", Stream: "filesystem", Type: "string", Kind: "string", Scope: "fs", Description: "挂载点路径"},
	{Name: "device", Stream: "filesystem", Type: "string", Kind: "string", Scope: "fs", Description: "挂载的设备或虚拟设备名"},
	{Name: "fstype", Stream: "filesystem", Type: "string", Kind: "enum", Scope: "fs", Description: "文件系统类型，如 ext4、xfs、overlay"},
	{Name: "size_bytes", Stream: "filesystem", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "fs", Description: "文件系统总容量"},
	{Name: "used_bytes", Stream: "filesystem", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "fs", Description: "已使用容量（总容量减去空闲块）"},
	{Name: "avail_bytes", Stream: "filesystem", Type: "integer", Unit: "bytes", Kind: "bytes", Scope: "fs", Description: "非特权用户可用容量（f_bavail）"},
	{Name: "inodes_total", Stream: "filesystem", Type: "integer", Kind: "gauge", Scope: "fs", Description: "inode 总数"},
	{Name: "inodes_free", Stream: "filesystem", Type: "integer", Kind: "gauge", Scope: "fs", Description: "空闲 inode 数"},
	{Name: "read_only", Stream: "filesystem", Type: "boolean", Kind: "enum", Scope: "fs", Description: "挂载是否为只读（ro）"},

	// network
	{Name: "iface", Stream: "network", Type: "string", Kind: "string", Scope: "iface", Description: "网络接口名，包含 lo"},
	{Name: "rx_bytes", Stream: "network", Type: "integer", Unit: "bytes", Kind: "counter", Scope: "iface", Description: "接口累计接收字节数"},
	{Name: "tx_bytes", Stream: "network", Type: "integer", Unit: "bytes", Kind: "counter", Scope: "iface", Description: "接口累计发送字节数"},
	{Name: "rx_packets", Stream: "network", Type: "integer", Unit: "个", Kind: "counter", Scope: "iface", Description: "接口累计接收报文数"},
	{Name: "tx_packets", Stream: "network", Type: "integer", Unit: "个", Kind: "counter", Scope: "iface", Description: "接口累计发送报文数"},
	{Name: "rx_errors", Stream: "network", Type: "integer", Unit: "个", Kind: "counter", Scope: "iface", Description: "接口累计接收错误数"},
	{Name: "tx_errors", Stream: "network", Type: "integer", Unit: "个", Kind: "counter", Scope: "iface", Description: "接口累计发送错误数"},
	{Name: "rx_dropped", Stream: "network", Type: "integer", Unit: "个", Kind: "counter", Scope: "iface", Description: "接口累计接收丢包数"},
	{Name: "tx_dropped", Stream: "network", Type: "integer", Unit: "个", Kind: "counter", Scope: "iface", Description: "接口累计发送丢包数"},
	{Name: "speed_mbps", Stream: "network", Type: "integer", Unit: "Mbps", Kind: "gauge", Scope: "iface", Description: "接口协商速率，读不到时为 0"},
	{Name: "carrier", Stream: "network", Type: "boolean", Kind: "enum", Scope: "iface", Description: "物理链路是否有载波"},
	{Name: "operstate", Stream: "network", Type: "string", Kind: "enum", Scope: "iface", Description: "接口运行状态，如 up、down、unknown"},

	// conntrack
	{Name: "entries", Stream: "conntrack", Type: "integer", Kind: "gauge", Scope: "net", Description: "当前连接跟踪表项数"},
	{Name: "max", Stream: "conntrack", Type: "integer", Kind: "gauge", Scope: "net", Description: "连接跟踪表最大表项数"},
	{Name: "insert_failed", Stream: "conntrack", Type: "integer", Kind: "counter", Scope: "net", Description: "连接跟踪表插入失败累计次数"},
	{Name: "drop", Stream: "conntrack", Type: "integer", Kind: "counter", Scope: "net", Description: "连接跟踪丢包累计次数"},
	{Name: "early_drop", Stream: "conntrack", Type: "integer", Kind: "counter", Scope: "net", Description: "表满时提前淘汰表项的累计次数"},
	{Name: "search_restart", Stream: "conntrack", Type: "integer", Kind: "counter", Scope: "net", Description: "连接跟踪查找重启累计次数"},
}
