package hw

import (
	"encoding/binary"
)

// smartReading 是与平台无关的 SMART 读数，nil 表示该字段不可用。
type smartReading struct {
	HealthOK              *bool
	TemperatureC          *int
	PowerOnHours          *int64
	PowerCycles           *int64
	UnsafeShutdowns       *int64
	ReallocatedSectorCt   *int64
	CurrentPendingSector  *int64
	OfflineUncorrectable  *int64
	UDMACRCErrorCount     *int64
	ReportedUncorrectable *int64
	MediaErrors           *int64
	PercentageUsed        *int
	AvailableSpare        *int
	CriticalWarning       *int
}

// ATA SMART 属性 ID（ATA-8 定义）。
const (
	ataAttrReallocatedSectorCt   = 5
	ataAttrPowerOnHours          = 9
	ataAttrPowerCycleCount       = 12
	ataAttrReportedUncorrectable = 187
	ataAttrTemperature190        = 190
	ataAttrUnsafeShutdowns       = 192
	ataAttrTemperature194        = 194
	ataAttrCurrentPendingSector  = 197
	ataAttrOfflineUncorrectable  = 198
	ataAttrUDMACRCErrorCount     = 199
)

// parseATASmartData 解析 ATA SMART READ DATA 返回的 512 字节。
// 属性表位于偏移 2，每项 12 字节（id/flags/value/worst/raw[6]/reserved）。
func parseATASmartData(buf []byte) smartReading {
	var r smartReading
	if len(buf) < 362 {
		return r
	}
	var temp190, temp194 *int
	for i := 0; i < 30; i++ {
		off := 2 + i*12
		if off+12 > len(buf) {
			break
		}
		id := buf[off]
		if id == 0 {
			continue
		}
		raw := buf[off+5 : off+11]
		switch id {
		case ataAttrReallocatedSectorCt:
			r.ReallocatedSectorCt = ptrInt64(rawLE(raw))
		case ataAttrPowerOnHours:
			r.PowerOnHours = ptrInt64(rawLE(raw))
		case ataAttrPowerCycleCount:
			r.PowerCycles = ptrInt64(rawLE(raw))
		case ataAttrReportedUncorrectable:
			r.ReportedUncorrectable = ptrInt64(rawLE(raw))
		case ataAttrTemperature190:
			temp190 = ptrInt(int(raw[0]))
		case ataAttrTemperature194:
			temp194 = ptrInt(int(raw[0]))
		case ataAttrUnsafeShutdowns:
			r.UnsafeShutdowns = ptrInt64(rawLE(raw))
		case ataAttrCurrentPendingSector:
			r.CurrentPendingSector = ptrInt64(rawLE(raw))
		case ataAttrOfflineUncorrectable:
			r.OfflineUncorrectable = ptrInt64(rawLE(raw))
		case ataAttrUDMACRCErrorCount:
			r.UDMACRCErrorCount = ptrInt64(rawLE(raw))
		}
	}
	// 194 是标准温度属性，190 是部分厂商的替代（气流温度），优先 194。
	if temp194 != nil {
		r.TemperatureC = temp194
	} else {
		r.TemperatureC = temp190
	}
	return r
}

// parseNVMeSmartLog 解析 NVMe SMART/Health Information log page（0x02）。
// 字段偏移见 NVMe Base Spec：critical_warning=0、温度(K)=1..2、
// available_spare=3、percentage_used=5；112 起为 128 位小端计数器：
// power_cycles=112、power_on_hours=128、unsafe_shutdowns=144、
// media_errors=160。日志被截断时只解析已存在的字段。
func parseNVMeSmartLog(b []byte) smartReading {
	var r smartReading
	if len(b) < 6 {
		return r
	}
	cw := int(b[0] & 0x0f)
	r.CriticalWarning = ptrInt(cw)
	r.HealthOK = ptrBool(cw == 0)

	if kelvin := int(binary.LittleEndian.Uint16(b[1:3])); kelvin > 0 {
		r.TemperatureC = ptrInt(kelvin - 273)
	}
	r.AvailableSpare = ptrInt(int(b[3]))
	r.PercentageUsed = ptrInt(int(b[5]))
	if len(b) >= 176 {
		if v, ok := rawLE128(b[112:128]); ok {
			r.PowerCycles = ptrInt64(v)
		}
		if v, ok := rawLE128(b[128:144]); ok {
			r.PowerOnHours = ptrInt64(v)
		}
		if v, ok := rawLE128(b[144:160]); ok {
			r.UnsafeShutdowns = ptrInt64(v)
		}
		if v, ok := rawLE128(b[160:176]); ok {
			r.MediaErrors = ptrInt64(v)
		}
	}
	return r
}

// nvmeCounterMax 是 64 位有符号计数的上限，128 位计数器饱和到此值。
const nvmeCounterMax int64 = 1<<63 - 1

// rawLE128 解析 NVMe 的 128 位小端计数器；超过 int64 范围时饱和。
func rawLE128(b []byte) (int64, bool) {
	if len(b) < 16 {
		return 0, false
	}
	lo := binary.LittleEndian.Uint64(b[:8])
	hi := binary.LittleEndian.Uint64(b[8:16])
	if hi != 0 || lo > uint64(nvmeCounterMax) {
		return nvmeCounterMax, true
	}
	return int64(lo), true
}

// rawLE 把最多 6 字节的小端原始值转成 int64。SMART raw 是 6 字节，
// 但常见厂商标注只使用低 4 字节，这里按实际已填充字节解析。
func rawLE(b []byte) int64 {
	var v int64
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<8 | int64(b[i])
	}
	return v
}
