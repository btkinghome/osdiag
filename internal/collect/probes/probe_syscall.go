package probes

import (
	"context"
	"time"

	"luanbird/osdiag/internal/collect"
)

// rawSyscallData 汇总三个原始系统调用的结果与耗时。
type rawSyscallData struct {
	UnameSysname string
	UnameRelease string
	UnameMachine string
	UnameErrno   string
	UnameDur     time.Duration

	PID      int
	PIDErrno string
	PIDDur   time.Duration

	RandomBytes  int
	RandomSource string
	RandomErrno  string
	RandomDur    time.Duration
}

// probeSyscall 执行 uname/getpid/getrandom 原始调用并记录每次 errno。
func probeSyscall(ctx context.Context, _ *collect.Env) outcome {
	r := newResult()
	if err := ctx.Err(); err != nil {
		markFail(r, errnoOf(err), "syscall 探测开始前已取消: "+err.Error())
		return outcome{res: r}
	}
	d := readRawSyscalls()

	r.Samples = append(r.Samples,
		sample(time.Now(), "uname", d.UnameDur, -1, d.UnameErrno),
		sample(time.Now(), "getpid", d.PIDDur, -1, d.PIDErrno),
		sample(time.Now(), "getrandom", d.RandomDur, int64(d.RandomBytes), d.RandomErrno),
	)

	r.Metrics = map[string]any{
		"uname_sysname":    d.UnameSysname,
		"uname_release":    d.UnameRelease,
		"uname_machine":    d.UnameMachine,
		"uname_ok":         d.UnameErrno == "",
		"uname_errno":      d.UnameErrno,
		"pid":              d.PID,
		"pid_ok":           d.PIDErrno == "",
		"getrandom_bytes":  d.RandomBytes,
		"getrandom_ok":     d.RandomErrno == "",
		"getrandom_errno":  d.RandomErrno,
		"getrandom_source": d.RandomSource,
	}

	switch {
	case d.UnameErrno != "":
		markFail(r, d.UnameErrno, "uname(2) 调用失败")
	case d.PIDErrno != "":
		markFail(r, d.PIDErrno, "getpid(2) 调用失败")
	case d.RandomErrno != "":
		markFail(r, d.RandomErrno, "getrandom(2) 调用失败")
	}
	return outcome{res: r}
}
