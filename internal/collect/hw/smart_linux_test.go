//go:build linux

package hw

import (
	"testing"
	"unsafe"
)

// TestIoctlStructSizes 复核 sgIoHdr / nvmeAdminCmd 的内核 ABI 尺寸。
// 编译期已用数组长度断言，这里给出可读的失败信息。
func TestIoctlStructSizes(t *testing.T) {
	if got := unsafe.Sizeof(sgIoHdr{}); got != 88 {
		t.Fatalf("sizeof(sg_io_hdr) = %d, want 88", got)
	}
	if got := unsafe.Sizeof(nvmeAdminCmd{}); got != 72 {
		t.Fatalf("sizeof(nvme_admin_cmd) = %d, want 72", got)
	}
	// 关键字段偏移与内核结构体一致。
	var h sgIoHdr
	if off := unsafe.Offsetof(h.Status); off != 64 {
		t.Fatalf("sg_io_hdr.status 偏移 = %d, want 64", off)
	}
	if off := unsafe.Offsetof(h.Resid); off != 72 {
		t.Fatalf("sg_io_hdr.resid 偏移 = %d, want 72", off)
	}
	var c nvmeAdminCmd
	if off := unsafe.Offsetof(c.Addr); off != 24 {
		t.Fatalf("nvme_admin_cmd.addr 偏移 = %d, want 24", off)
	}
	if off := unsafe.Offsetof(c.Cdw10); off != 40 {
		t.Fatalf("nvme_admin_cmd.cdw10 偏移 = %d, want 40", off)
	}
	if off := unsafe.Offsetof(c.TimeoutMs); off != 64 {
		t.Fatalf("nvme_admin_cmd.timeout_ms 偏移 = %d, want 64", off)
	}
}
