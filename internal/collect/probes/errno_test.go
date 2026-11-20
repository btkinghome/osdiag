package probes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	"luanbird/osdiag/internal/model"
)

func TestErrnoOf(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"显式 errno", newErrnoError("EACCES", "权限不足"), "EACCES"},
		{"包装的 errnoError", fmt.Errorf("外层: %w", newErrnoError("EPERM", "x")), "EPERM"},
		{"context 超时", context.DeadlineExceeded, "ETIMEDOUT"},
		{"context 取消", context.Canceled, "ECANCELED"},
		{"DNS 超时", &net.DNSError{IsTimeout: true}, "ETIMEDOUT"},
		{"DNS 不存在", &net.DNSError{IsNotFound: true}, "ENOTFOUND"},
		{"DNS 临时失败", &net.DNSError{IsTemporary: true}, "EDNSTEMP"},
		{"DNS 其他", &net.DNSError{}, "EDNS"},
		{"syscall 已知", syscall.ECONNREFUSED, "ECONNREFUSED"},
		{"syscall 包装", fmt.Errorf("连接失败: %w", syscall.EHOSTUNREACH), "EHOSTUNREACH"},
		{"syscall 未知", syscall.Errno(0xdead), "ERRNO_57005"},
		{"真实超时", &net.OpError{Op: "read", Err: &timeoutError{}}, "ETIMEDOUT"},
		{"完全未知", errors.New("boom"), "EUNKNOWN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errnoOf(tc.err); got != tc.want {
				t.Errorf("errnoOf(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

type timeoutError struct{}

func (*timeoutError) Error() string   { return "timeout" }
func (*timeoutError) Timeout() bool   { return true }
func (*timeoutError) Temporary() bool { return true }

func TestFinalizeContract(t *testing.T) {
	start := time.Now().Add(-2 * time.Millisecond)

	res := newResult()
	res.OK = true
	res.Skipped = true
	res.Error = &model.ProbeError{Errno: "EACCES", Msg: "不应出现"}
	finalize(res, "dns.localhost", "P0", start)
	if res.OK || !res.Skipped {
		t.Errorf("skipped 结果应 ok=false skipped=true: %+v", res)
	}
	if res.Error != nil {
		t.Errorf("skipped 结果不应保留 error: %+v", res.Error)
	}
	if res.Probe != "dns.localhost" || res.Level != "P0" || res.SchemaVersion == "" {
		t.Errorf("finalize 公共字段不完整: %+v", res)
	}
	if res.SkipReason != "skipped" {
		t.Errorf("空 skip_reason 应兜底为 skipped，得到 %q", res.SkipReason)
	}

	failed := newResult()
	failed.OK = false
	finalize(failed, "syscall.basic", "P0", start)
	if failed.Error == nil || failed.Error.Errno != "EUNKNOWN" || failed.Error.Msg == "" {
		t.Errorf("ok=false 且无 error 时应兜底: %+v", failed.Error)
	}

	ok := newResult()
	finalize(ok, "net.loopback", "P0", start)
	if !ok.OK || ok.Error != nil || ok.DurationMS < 0 {
		t.Errorf("成功结果不应带 error: %+v", ok)
	}
}
