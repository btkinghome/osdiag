package probes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
)

// errnoError 让探测内部错误携带明确的 errno 码。
type errnoError struct {
	code string
	msg  string
}

func (e *errnoError) Error() string { return e.msg }

func newErrnoError(code, msg string) error { return &errnoError{code: code, msg: msg} }

// errnoOf 把 Go error 归一化为可读的 errno 字符串。
// 探测只记录动作错误，不做系统故障推断。
func errnoOf(err error) string {
	if err == nil {
		return ""
	}
	var ee *errnoError
	if errors.As(err, &ee) {
		return ee.code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "ETIMEDOUT"
	}
	if errors.Is(err, context.Canceled) {
		return "ECANCELED"
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		switch {
		case dnsErr.IsTimeout:
			return "ETIMEDOUT"
		case dnsErr.IsNotFound:
			return "ENOTFOUND"
		case dnsErr.IsTemporary:
			return "EDNSTEMP"
		default:
			return "EDNS"
		}
	}

	var errno syscall.Errno
	if errors.As(err, &errno) {
		if name, ok := errnoNames[errno]; ok {
			return name
		}
		return fmt.Sprintf("ERRNO_%d", int(errno))
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "ETIMEDOUT"
	}

	return "EUNKNOWN"
}

// errnoNames 覆盖常见探测错误。注意 EAGAIN/EWOULDBLOCK 与
// ENOTSUP/EOPNOTSUPP 在部分平台取值相同，只保留一个以免重复常量。
var errnoNames = map[syscall.Errno]string{
	syscall.EPERM:        "EPERM",
	syscall.EACCES:       "EACCES",
	syscall.ENOENT:       "ENOENT",
	syscall.EBADF:        "EBADF",
	syscall.EINTR:        "EINTR",
	syscall.EIO:          "EIO",
	syscall.EAGAIN:       "EAGAIN",
	syscall.ENOMEM:       "ENOMEM",
	syscall.EFAULT:       "EFAULT",
	syscall.EBUSY:        "EBUSY",
	syscall.EEXIST:       "EEXIST",
	syscall.ENOTDIR:      "ENOTDIR",
	syscall.EISDIR:       "EISDIR",
	syscall.EINVAL:       "EINVAL",
	syscall.EMFILE:       "EMFILE",
	syscall.ENFILE:       "ENFILE",
	syscall.ENOSPC:       "ENOSPC",
	syscall.EROFS:        "EROFS",
	syscall.ELOOP:        "ELOOP",
	syscall.EPIPE:        "EPIPE",
	syscall.ECONNREFUSED: "ECONNREFUSED",
	syscall.ECONNRESET:   "ECONNRESET",
	syscall.EHOSTUNREACH: "EHOSTUNREACH",
	syscall.ENETUNREACH:  "ENETUNREACH",
	syscall.ETIMEDOUT:    "ETIMEDOUT",
	syscall.EADDRINUSE:   "EADDRINUSE",
	syscall.EOPNOTSUPP:   "EOPNOTSUPP",
	syscall.ECANCELED:    "ECANCELED",
	syscall.ENOTTY:       "ENOTTY",
	syscall.EOVERFLOW:    "EOVERFLOW",
}
