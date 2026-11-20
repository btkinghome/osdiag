package probes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"luanbird/osdiag/internal/model"
)

// eventLog 逐行追加 probes/runlog.jsonl。探测可能并发（超时后的 goroutine 仍在
// 收尾），因此用互斥锁保护单行写入。
type eventLog struct {
	mu sync.Mutex
	f  *os.File
}

func openEventLog(path string) (*eventLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &eventLog{f: f}, nil
}

func (l *eventLog) emit(ev model.RunlogEvent) error {
	ev.SchemaVersion = model.SchemaVersion
	if ev.TS == "" {
		ev.TS = formatTS(time.Now())
	}
	ev.Detail = truncate(ev.Detail, 500)
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.f.Write(line)
	return err
}

func (l *eventLog) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

func startEvent(id, level string) model.RunlogEvent {
	return model.RunlogEvent{Probe: id, Event: "start", Level: level, TS: formatTS(time.Now())}
}

func endEvent(id, level string, ms float64) model.RunlogEvent {
	return model.RunlogEvent{Probe: id, Event: "end", Level: level, DurationMS: durationPtr(ms), TS: formatTS(time.Now())}
}

func skipEvent(id, level, reason string, ms float64) model.RunlogEvent {
	return model.RunlogEvent{
		Probe: id, Event: "skip", Level: level, Detail: reason,
		DurationMS: durationPtr(ms), TS: formatTS(time.Now()),
	}
}

func errorEvent(id, level, detail string) model.RunlogEvent {
	return model.RunlogEvent{Probe: id, Event: "error", Level: level, Detail: detail, TS: formatTS(time.Now())}
}

func cleanupEvent(id, level, detail string) model.RunlogEvent {
	return model.RunlogEvent{Probe: id, Event: "cleanup", Level: level, Detail: detail, TS: formatTS(time.Now())}
}
