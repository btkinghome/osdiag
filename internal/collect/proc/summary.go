package proc

import (
	"runtime"
	"sort"
	"time"

	"luanbird/osdiag/internal/model"
)

const (
	// userHZ 是 Linux 用户态 CLK_TCK。x86_64/aarch64（以及主流 32 位 Linux）
	// 均为 100，/proc 的 utime/stime/delayacct 均以该单位计。
	userHZ = 100.0
	topN   = 20
)

// buildSummary 用结束快照样本与开始快照计算 top/summary.json 的内容。
// CPU 增量只对 start/end 两次都存在的 pid 计算；墙上时间用于识别 PID 复用/
// 计数器异常（CPU 时间不可能超过 墙钟 × 核数）。
func buildSummary(samples []procSample, start *snapshot, endTS time.Time, ncpu, listening int) model.TopSummary {
	if ncpu <= 0 {
		ncpu = runtime.NumCPU()
	}
	if ncpu <= 0 {
		ncpu = 1
	}

	summary := model.TopSummary{
		SchemaVersion: model.SchemaVersion,
		GeneratedAt:   endTS.Format(time.RFC3339),
		Counts: model.TopCounts{
			ListeningSockets: listening,
		},
	}

	wallSec := 0.0
	if start != nil && !start.ts.IsZero() {
		wallSec = endTS.Sub(start.ts).Seconds()
	}

	var cpuList []model.TopProcCPU
	var rssList []model.TopProcRSS
	var ioList []model.TopProcIO
	var dstate []model.DStateProc

	for _, p := range samples {
		summary.Counts.Processes++
		summary.Counts.Threads += p.Threads
		if p.CmdlineEmpty && p.PPID == 2 {
			summary.Counts.KernelThreads++
		}
		switch p.State {
		case "Z":
			summary.Counts.Zombies++
		case "D":
			summary.Counts.DState++
		}

		if p.RSSBytes > 0 {
			rssList = append(rssList, model.TopProcRSS{
				PID: p.PID, Comm: p.Comm, Cmdline: p.Cmdline, RSSBytes: p.RSSBytes,
			})
		}
		// top_io 展示"本窗口内的 IO 活动"：开始快照里已有的进程取增量；
		// 窗口内新创建的进程没有开始记录，其累计值本身就落在窗口内。
		ioRead, ioWrite, haveIO := p.IORead, p.IOWrite, p.IORead+p.IOWrite > 0
		if start != nil {
			if prev, ok := start.procs[p.PID]; ok {
				ioRead, ioWrite, haveIO = ioDelta(prev, p)
			}
		}
		if haveIO {
			ioList = append(ioList, model.TopProcIO{
				PID: p.PID, Comm: p.Comm, Cmdline: p.Cmdline,
				ReadBytes: ioRead, WriteBytes: ioWrite,
			})
		}
		if p.State == "D" {
			dstate = append(dstate, model.DStateProc{
				PID: p.PID, Comm: p.Comm, State: p.State, Cmdline: p.Cmdline,
				Wchan: p.Wchan, DelayBlkio: p.DelayBlkio,
			})
		}

		if start != nil {
			if prev, ok := start.procs[p.PID]; ok {
				if ms, ok := cpuMillis(prev, p, wallSec, ncpu); ok {
					cpuList = append(cpuList, model.TopProcCPU{
						PID: p.PID, Comm: p.Comm, Cmdline: p.Cmdline, CPUMs: ms,
					})
				}
			}
		}
	}

	sort.Slice(cpuList, func(i, j int) bool {
		if cpuList[i].CPUMs != cpuList[j].CPUMs {
			return cpuList[i].CPUMs > cpuList[j].CPUMs
		}
		return cpuList[i].PID < cpuList[j].PID
	})
	sort.Slice(rssList, func(i, j int) bool {
		if rssList[i].RSSBytes != rssList[j].RSSBytes {
			return rssList[i].RSSBytes > rssList[j].RSSBytes
		}
		return rssList[i].PID < rssList[j].PID
	})
	sort.Slice(ioList, func(i, j int) bool {
		si := ioList[i].ReadBytes + ioList[i].WriteBytes
		sj := ioList[j].ReadBytes + ioList[j].WriteBytes
		if si != sj {
			return si > sj
		}
		return ioList[i].PID < ioList[j].PID
	})
	sort.Slice(dstate, func(i, j int) bool { return dstate[i].PID < dstate[j].PID })

	summary.TopCPU = capCPU(cpuList)
	summary.TopRSS = capRSS(rssList)
	summary.TopIO = capIO(ioList)
	summary.DState = dstate
	return summary
}

// ioDelta 返回两次快照之间的 read_bytes/write_bytes 增量；计数器回退
// （PID 复用或异常）或没有增量时 ok=false。
func ioDelta(prev cpuSnap, end procSample) (read, write uint64, ok bool) {
	if end.IORead < prev.ioRead || end.IOWrite < prev.ioWrite {
		return 0, 0, false
	}
	read = end.IORead - prev.ioRead
	write = end.IOWrite - prev.ioWrite
	if read+write == 0 {
		return 0, 0, false
	}
	return read, write, true
}

// cpuMillis 用 utime+stime 的 tick 差换算 CPU 毫秒；无法可信计算时返回 ok=false。
func cpuMillis(prev cpuSnap, end procSample, wallSec float64, ncpu int) (float64, bool) {
	if end.Utime < prev.utime || end.Stime < prev.stime {
		return 0, false // 计数器回退：PID 复用或数据竞争
	}
	delta := (end.Utime - prev.utime) + (end.Stime - prev.stime)
	if delta == 0 {
		return 0, false
	}
	// 两个快照之间 CPU 时间不可能超过 墙钟 × 核数；超出视为 PID 复用。
	if wallSec > 0 && float64(delta) > wallSec*float64(ncpu)*userHZ {
		return 0, false
	}
	return float64(delta) * 1000.0 / userHZ, true
}

func capCPU(in []model.TopProcCPU) []model.TopProcCPU {
	if len(in) > topN {
		return in[:topN]
	}
	return in
}

func capRSS(in []model.TopProcRSS) []model.TopProcRSS {
	if len(in) > topN {
		return in[:topN]
	}
	return in
}

func capIO(in []model.TopProcIO) []model.TopProcIO {
	if len(in) > topN {
		return in[:topN]
	}
	return in
}
