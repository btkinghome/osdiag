//go:build linux

package hw

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// SG_IO 与 NVMe admin passthrough 相关常量。
// 参考 linux/scsi/sg.h、linux/nvme_ioctl.h 以及 SAT-2 规范。
// ATA PASS-THROUGH 的 CDB 构造见 smart_ata.go（可在任意平台测试）。
const (
	sgIO                = 0x2285     // SG_IO ioctl 号
	sgDxferFromDevice   = -1         // SG_DXFER_FROM_DEV
	sgInterfaceIDScsi   = 'S'        // sg_io_hdr.interface_id
	smartLogBufSize     = 512        // 一个 ATA/NVMe 扇区
	nvmeIoctlAdminCmd   = 0xc0484e41 // _IOWR('N', 0x41, struct nvme_admin_cmd)
	nvmeAdminGetLogPage = 0x02       // Admin Get Log Page
	nvmeLogSmartHealth  = 0x02       // SMART / Health Information log
	nvmeNamespaceAll    = 0xffffffff // 控制器级日志使用全 1 namespace
)

// sgIoHdr 对应 scsi/sg.h 的 sg_io_hdr_t。linux/amd64 与 arm64 上均为 88 字节，
// 字段顺序必须与内核 ABI 一致。
type sgIoHdr struct {
	InterfaceID    int32          // 'S'
	DxferDirection int32          // SG_DXFER_FROM_DEV = -1
	CmdLen         uint8          // CDB 长度
	MxSbLen        uint8          // sense 缓冲区长度
	IovecCount     uint16         // 未使用
	DxferLen       uint32         // 数据传输长度
	Dxferp         unsafe.Pointer // 数据缓冲区
	Cmdp           unsafe.Pointer // CDB 缓冲区
	Sbp            unsafe.Pointer // sense 缓冲区
	Timeout        uint32         // 超时（毫秒）
	Flags          uint32         // 未使用
	PackID         int32          // 未使用
	UsrPtr         unsafe.Pointer // 未使用
	Status         uint8          // SCSI 状态
	MaskedStatus   uint8
	MsgStatus      uint8
	SbLenWr        uint8 // 实际写入的 sense 字节数
	HostStatus     uint16
	DriverStatus   uint16
	Resid          int32  // 剩余未传输字节
	Duration       uint32 // 命令耗时（毫秒）
	Info           uint32
}

// nvmeAdminCmd 对应 linux/nvme_ioctl.h 的 struct nvme_admin_cmd，72 字节。
type nvmeAdminCmd struct {
	Opcode      uint8
	Flags       uint8
	Rsvd1       uint16
	NSID        uint32
	Cdw2        uint32
	Cdw3        uint32
	Metadata    uint64
	Addr        uint64
	MetadataLen uint32
	DataLen     uint32
	Cdw10       uint32
	Cdw11       uint32
	Cdw12       uint32
	Cdw13       uint32
	Cdw14       uint32
	Cdw15       uint32
	TimeoutMs   uint32
	Result      uint32
}

// 编译期断言：ioctl 结构体尺寸必须与内核 ABI 完全一致。
// 若尺寸不符，数组长度变为负数，编译失败。
var (
	_ [88 - int(unsafe.Sizeof(sgIoHdr{}))]byte
	_ [int(unsafe.Sizeof(sgIoHdr{})) - 88]byte
	_ [72 - int(unsafe.Sizeof(nvmeAdminCmd{}))]byte
	_ [int(unsafe.Sizeof(nvmeAdminCmd{})) - 72]byte
)

// probeSMARTDevice 在 Linux 上按设备类型分派到 ATA / NVMe 实现。
func probeSMARTDevice(ctx context.Context, path, kind string, timeout time.Duration) (smartReading, error) {
	if err := ctx.Err(); err != nil {
		return smartReading{}, err
	}
	switch kind {
	case "ata":
		return probeATASMART(path, timeout)
	case "nvme":
		return probeNVMeSMART(path, timeout)
	default:
		return smartReading{}, fmt.Errorf("%w: 未知设备类型 %q", errSMARTUnsupported, kind)
	}
}

// probeATASMART 通过 SG_IO + ATA PASS-THROUGH(16) 发送 SMART READ DATA(0xD0)。
// O_NONBLOCK 打开设备，避免无介质/休眠设备阻塞；超时由内核 SG_IO 保证。
func probeATASMART(path string, timeout time.Duration) (smartReading, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return smartReading{}, fmt.Errorf("打开 %s 失败: %w", path, err)
	}
	defer f.Close()
	// 先取 fd：后续填好 CDB/缓冲区后到 syscall 之间不能再有函数调用，
	// 以免栈增长使缓冲区地址失效（SG_IO 结构体内为类型化指针，仍按此约定编写）。
	fd := f.Fd()

	var buf [smartLogBufSize]byte
	var sense [32]byte
	cdb := ataSmartReadDataCDB()

	hdr := sgIoHdr{
		InterfaceID:    sgInterfaceIDScsi,
		DxferDirection: sgDxferFromDevice,
		CmdLen:         16,
		MxSbLen:        uint8(len(sense)),
		DxferLen:       uint32(len(buf)),
		Dxferp:         unsafe.Pointer(&buf[0]),
		Cmdp:           unsafe.Pointer(&cdb[0]),
		Sbp:            unsafe.Pointer(&sense[0]),
		Timeout:        uint32(timeout / time.Millisecond),
	}
	if hdr.Timeout == 0 {
		hdr.Timeout = 1000
	}

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, sgIO, uintptr(unsafe.Pointer(&hdr)))
	switch {
	case errno != 0:
		return smartReading{}, fmt.Errorf("SG_IO(%s) 失败: %w", path, errno)
	case hdr.Status != 0 || hdr.HostStatus != 0 || hdr.DriverStatus != 0:
		n := int(hdr.SbLenWr)
		if n > len(sense) {
			n = len(sense)
		}
		return smartReading{}, fmt.Errorf(
			"ATA SMART READ DATA(%s) 失败: status=0x%02x host=0x%04x driver=0x%04x sense=% x",
			path, hdr.Status, hdr.HostStatus, hdr.DriverStatus, sense[:n])
	case hdr.Resid != 0:
		return smartReading{}, fmt.Errorf("ATA SMART 数据不完整(%s): resid=%d", path, hdr.Resid)
	}
	return parseATASmartData(buf[:]), nil
}

// probeNVMeSMART 通过 NVME_IOCTL_ADMIN_CMD 读取 SMART/Health Information
// log page（0x02），返回 512 字节。
func probeNVMeSMART(path string, timeout time.Duration) (smartReading, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return smartReading{}, fmt.Errorf("打开 %s 失败: %w", path, err)
	}
	defer f.Close()
	fd := f.Fd()

	var buf [smartLogBufSize]byte
	cmd := nvmeAdminCmd{
		Opcode:    nvmeAdminGetLogPage,
		NSID:      nvmeNamespaceAll,
		DataLen:   smartLogBufSize,
		Cdw10:     nvmeLogSmartHealth | (127 << 16), // NUMD=127 -> 128 dwords
		TimeoutMs: uint32(timeout / time.Millisecond),
	}
	if cmd.TimeoutMs == 0 {
		cmd.TimeoutMs = 1000
	}
	// Addr 必须在调用 syscall 前最后写入，中间不能插入函数调用，
	// 否则栈增长后 buf 地址可能与实际位置不符。
	cmd.Addr = uint64(uintptr(unsafe.Pointer(&buf[0])))

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, nvmeIoctlAdminCmd, uintptr(unsafe.Pointer(&cmd)))
	if errno != 0 {
		return smartReading{}, fmt.Errorf("NVME_IOCTL_ADMIN_CMD(%s) 失败: %w", path, errno)
	}
	return parseNVMeSmartLog(buf[:]), nil
}
