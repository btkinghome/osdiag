// Package collect 提供采集器的共享上下文与输出约定。
package collect

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"luanbird/osdiag/internal/model"
)

// Env 是一次诊断运行的共享上下文。所有采集器只通过 Env 暴露的方法
// 记录错误、标记、探测目标与覆盖率，避免各自改写 manifest。
type Env struct {
	Stage       string
	RunID       string
	Workdir     string
	Profile     string
	Start       time.Time
	Duration    time.Duration
	AllowHeavy  bool
	AllowExtern bool
	NoProbe     bool
	JournalFile string
	WithEnviron bool
	Host        model.Host

	mu       sync.Mutex
	errs     []model.RunError
	markers  []model.Marker
	targets  []model.NetTarget
	coverage model.Coverage
	enabled  []string
	skipped  []string
	counts   model.Counts
	probeRun bool
}

// NewEnv 创建采集上下文。
func NewEnv(stage, runID, workdir, profile string, start time.Time, duration time.Duration) *Env {
	return &Env{
		Stage:    stage,
		RunID:    runID,
		Workdir:  workdir,
		Profile:  profile,
		Start:    start,
		Duration: duration,
		coverage: model.Coverage{Gaps: []model.Gap{}},
	}
}

// Path 返回包内相对路径对应的绝对路径。
func (e *Env) Path(rel string) string { return filepath.Join(e.Stage, filepath.FromSlash(rel)) }

// Deadlin 返回本次采样周期的结束时间。
func (e *Env) Deadlin() time.Time { return e.Start.Add(e.Duration) }

func (e *Env) AddError(collector, format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errs = append(e.errs, model.RunError{Collector: collector, Msg: fmt.Sprintf(format, args...)})
}

func (e *Env) Errors() []model.RunError {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]model.RunError, len(e.errs))
	copy(out, e.errs)
	return out
}

func (e *Env) AddMarker(kind, format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.markers = append(e.markers, model.Marker{
		TS:      time.Now().UTC().Format(time.RFC3339),
		Kind:    kind,
		Message: fmt.Sprintf(format, args...),
	})
}

func (e *Env) Markers() []model.Marker {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]model.Marker, len(e.markers))
	copy(out, e.markers)
	return out
}

func (e *Env) AddNetTarget(t model.NetTarget) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.targets = append(e.targets, t)
}

func (e *Env) NetTargets() []model.NetTarget {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]model.NetTarget, len(e.targets))
	copy(out, e.targets)
	return out
}

func (e *Env) SetCoverage(c model.Coverage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c.Gaps == nil {
		c.Gaps = []model.Gap{}
	}
	e.coverage = c
}

func (e *Env) Coverage() model.Coverage {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.coverage
	c.Gaps = append([]model.Gap{}, e.coverage.Gaps...)
	return c
}

// EnableProbe 记录实际执行的探测 ID。
func (e *Env) EnableProbe(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enabled = append(e.enabled, id)
}

// SkipProbe 记录因压力/开关/依赖缺失而跳过的探测。
func (e *Env) SkipProbe(id, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.skipped = append(e.skipped, fmt.Sprintf("%s: %s", id, reason))
}

func (e *Env) EnabledProbes() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.enabled...)
}

func (e *Env) SkippedProbes() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.skipped...)
}

func (e *Env) SetProbeRun() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.probeRun = true
}

func (e *Env) ProbeRun() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.probeRun
}

func (e *Env) SetCounts(c model.Counts) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.counts = c
}

func (e *Env) Counts() model.Counts {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.counts
}
