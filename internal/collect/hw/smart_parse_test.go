package hw

import (
	"encoding/binary"
	"testing"
)

// setATAAttr 在 SMART READ DATA 缓冲区中写入一个属性项。
func setATAAttr(t *testing.T, buf []byte, idx int, id byte, raw [6]byte) {
	t.Helper()
	off := 2 + idx*12
	if off+12 > len(buf) {
		t.Fatalf("属性 %d 越界: off=%d len=%d", idx, off, len(buf))
	}
	buf[off] = id
	buf[off+1] = 0x00
	buf[off+2] = 100
	buf[off+3] = 100
	copy(buf[off+5:off+11], raw[:])
}

func TestParseATASmartData(t *testing.T) {
	t.Run("常见字段", func(t *testing.T) {
		buf := make([]byte, 512)
		setATAAttr(t, buf, 0, 5, [6]byte{7, 0, 0, 0, 0, 0})        // reallocated
		setATAAttr(t, buf, 1, 9, [6]byte{0x10, 0x27, 0, 0, 0, 0})  // power_on_hours = 10000
		setATAAttr(t, buf, 2, 12, [6]byte{0x2c, 0x01, 0, 0, 0, 0}) // power_cycles = 300
		setATAAttr(t, buf, 3, 187, [6]byte{2, 0, 0, 0, 0, 0})      // reported_uncorrectable
		setATAAttr(t, buf, 4, 190, [6]byte{40, 0, 0, 0, 0, 0})     // 旧温度属性
		setATAAttr(t, buf, 5, 194, [6]byte{35, 0, 0, 0, 0, 0})     // 标准温度属性
		setATAAttr(t, buf, 6, 192, [6]byte{3, 0, 0, 0, 0, 0})      // unsafe_shutdowns
		setATAAttr(t, buf, 7, 197, [6]byte{1, 0, 0, 0, 0, 0})      // current_pending
		setATAAttr(t, buf, 8, 198, [6]byte{4, 0, 0, 0, 0, 0})      // offline_uncorrectable
		setATAAttr(t, buf, 9, 199, [6]byte{5, 0, 0, 0, 0, 0})      // udma_crc
		setATAAttr(t, buf, 10, 0, [6]byte{9, 9, 9, 9, 9, 9})       // 空属性应忽略

		got := parseATASmartData(buf)
		if got.HealthOK != nil {
			t.Errorf("ATA HealthOK = %v, want nil", got.HealthOK)
		}
		if got.TemperatureC == nil || *got.TemperatureC != 35 {
			t.Errorf("temperature = %v, want 35（优先 194）", got.TemperatureC)
		}
		if int64Or(got.PowerOnHours, -1) != 10000 {
			t.Errorf("power_on_hours = %v, want 10000", got.PowerOnHours)
		}
		if int64Or(got.PowerCycles, -1) != 300 {
			t.Errorf("power_cycles = %v, want 300", got.PowerCycles)
		}
		if int64Or(got.ReallocatedSectorCt, -1) != 7 {
			t.Errorf("reallocated = %v, want 7", got.ReallocatedSectorCt)
		}
		if int64Or(got.ReportedUncorrectable, -1) != 2 {
			t.Errorf("reported_uncorrectable = %v, want 2", got.ReportedUncorrectable)
		}
		if int64Or(got.UnsafeShutdowns, -1) != 3 {
			t.Errorf("unsafe_shutdowns = %v, want 3", got.UnsafeShutdowns)
		}
		if int64Or(got.CurrentPendingSector, -1) != 1 {
			t.Errorf("current_pending = %v, want 1", got.CurrentPendingSector)
		}
		if int64Or(got.OfflineUncorrectable, -1) != 4 {
			t.Errorf("offline_uncorrectable = %v, want 4", got.OfflineUncorrectable)
		}
		if int64Or(got.UDMACRCErrorCount, -1) != 5 {
			t.Errorf("udma_crc = %v, want 5", got.UDMACRCErrorCount)
		}
	})

	t.Run("6 字节 raw 大值", func(t *testing.T) {
		buf := make([]byte, 512)
		// 0x1122334455 的小端表示。
		setATAAttr(t, buf, 0, 9, [6]byte{0x55, 0x44, 0x33, 0x22, 0x11, 0x00})
		got := parseATASmartData(buf)
		const want int64 = 0x1122334455
		if int64Or(got.PowerOnHours, -1) != want {
			t.Errorf("power_on_hours = %v, want %d", got.PowerOnHours, want)
		}
	})

	t.Run("只有 190 温度", func(t *testing.T) {
		buf := make([]byte, 512)
		setATAAttr(t, buf, 0, 190, [6]byte{41, 0, 0, 0, 0, 0})
		got := parseATASmartData(buf)
		if got.TemperatureC == nil || *got.TemperatureC != 41 {
			t.Errorf("temperature = %v, want 41（回退 190）", got.TemperatureC)
		}
	})

	t.Run("缓冲区过短", func(t *testing.T) {
		got := parseATASmartData(make([]byte, 100))
		if got.TemperatureC != nil || got.PowerOnHours != nil || got.ReallocatedSectorCt != nil {
			t.Errorf("短缓冲区应返回空读数, got %+v", got)
		}
	})
}

func TestParseNVMeSmartLog(t *testing.T) {
	t.Run("完整日志", func(t *testing.T) {
		buf := make([]byte, 512)
		buf[0] = 0x02 // critical_warning = 2
		binary.LittleEndian.PutUint16(buf[1:3], 273+38)
		buf[3] = 100                                       // available_spare
		buf[5] = 7                                         // percentage_used
		binary.LittleEndian.PutUint64(buf[112:120], 1000)  // power_cycles
		binary.LittleEndian.PutUint64(buf[128:136], 20000) // power_on_hours
		binary.LittleEndian.PutUint64(buf[144:152], 42)    // unsafe_shutdowns
		binary.LittleEndian.PutUint64(buf[160:168], 5)     // media_errors

		got := parseNVMeSmartLog(buf)
		if got.CriticalWarning == nil || *got.CriticalWarning != 2 {
			t.Errorf("critical_warning = %v, want 2", got.CriticalWarning)
		}
		if got.HealthOK == nil || *got.HealthOK {
			t.Errorf("health_ok = %v, want false", got.HealthOK)
		}
		if got.TemperatureC == nil || *got.TemperatureC != 38 {
			t.Errorf("temperature = %v, want 38", got.TemperatureC)
		}
		if got.AvailableSpare == nil || *got.AvailableSpare != 100 {
			t.Errorf("available_spare = %v, want 100", got.AvailableSpare)
		}
		if got.PercentageUsed == nil || *got.PercentageUsed != 7 {
			t.Errorf("percentage_used = %v, want 7", got.PercentageUsed)
		}
		if int64Or(got.PowerCycles, -1) != 1000 {
			t.Errorf("power_cycles = %v, want 1000", got.PowerCycles)
		}
		if int64Or(got.PowerOnHours, -1) != 20000 {
			t.Errorf("power_on_hours = %v, want 20000", got.PowerOnHours)
		}
		if int64Or(got.UnsafeShutdowns, -1) != 42 {
			t.Errorf("unsafe_shutdowns = %v, want 42", got.UnsafeShutdowns)
		}
		if int64Or(got.MediaErrors, -1) != 5 {
			t.Errorf("media_errors = %v, want 5", got.MediaErrors)
		}
	})

	t.Run("critical_warning 只取低 4 位", func(t *testing.T) {
		buf := make([]byte, 88)
		buf[0] = 0xf2 // 低 4 位为 2
		binary.LittleEndian.PutUint16(buf[1:3], 273+30)
		got := parseNVMeSmartLog(buf)
		if got.CriticalWarning == nil || *got.CriticalWarning != 2 {
			t.Errorf("critical_warning = %v, want 2", got.CriticalWarning)
		}
		if got.HealthOK == nil || *got.HealthOK {
			t.Errorf("health_ok = %v, want false", got.HealthOK)
		}
	})

	t.Run("零温度表示不可用", func(t *testing.T) {
		buf := make([]byte, 176)
		binary.LittleEndian.PutUint64(buf[128:136], 100)
		got := parseNVMeSmartLog(buf)
		if got.TemperatureC != nil {
			t.Errorf("temperature = %v, want nil", got.TemperatureC)
		}
		if got.HealthOK == nil || !*got.HealthOK {
			t.Errorf("health_ok = %v, want true", got.HealthOK)
		}
		if int64Or(got.PowerOnHours, -1) != 100 {
			t.Errorf("power_on_hours = %v, want 100", got.PowerOnHours)
		}
	})

	t.Run("日志过短", func(t *testing.T) {
		got := parseNVMeSmartLog(make([]byte, 5))
		if got.HealthOK != nil || got.PowerOnHours != nil || got.MediaErrors != nil {
			t.Errorf("短日志应返回空读数, got %+v", got)
		}
	})

	t.Run("计数器区被截断", func(t *testing.T) {
		buf := make([]byte, 87)
		binary.LittleEndian.PutUint16(buf[1:3], 273+30)
		got := parseNVMeSmartLog(buf)
		if got.HealthOK == nil || !*got.HealthOK {
			t.Errorf("health_ok = %v, want true", got.HealthOK)
		}
		if got.TemperatureC == nil || *got.TemperatureC != 30 {
			t.Errorf("temperature = %v, want 30", got.TemperatureC)
		}
		if got.PowerOnHours != nil || got.MediaErrors != nil {
			t.Errorf("截断日志不应有计数器, got %+v", got)
		}
	})
}

func TestRawLE(t *testing.T) {
	tests := []struct {
		in   []byte
		want int64
	}{
		{[]byte{0x01}, 1},
		{[]byte{0xff, 0x00}, 255},
		{[]byte{0x00, 0x01}, 256},
		{[]byte{0x55, 0x44, 0x33, 0x22, 0x11}, 0x1122334455},
		{nil, 0},
	}
	for _, tc := range tests {
		if got := rawLE(tc.in); got != tc.want {
			t.Errorf("rawLE(% x) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestATASmartReadDataCDB(t *testing.T) {
	// 期望值与 smartmontools sat_device::ata_pass_through 生成的 CDB 一致：
	// ATA PASS-THROUGH(16)、PIO data-in、1 个扇区、SMART READ DATA。
	want := [16]byte{
		0x85, 0x08, 0x0e, 0x00,
		0xd0, 0x00, 0x01, 0x00,
		0x00, 0x00, 0x4f, 0x00,
		0xc2, 0x00, 0xb0, 0x00,
	}
	if got := ataSmartReadDataCDB(); got != want {
		t.Errorf("ataSmartReadDataCDB() = % x, want % x", got, want)
	}
}
