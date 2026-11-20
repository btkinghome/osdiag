package hw

// ATA PASS-THROUGH(16) 与 SMART READ DATA 的 CDB 布局。
// CDB 中的寄存器字段为大端：FEATURES 在 cdb[3:5]、SECTOR COUNT 在 cdb[5:7]、
// LBA LOW 在 cdb[7:9]、LBA MID 在 cdb[9:11]、LBA HIGH 在 cdb[11:13]、
// DEVICE 在 cdb[13]、COMMAND 在 cdb[14]。
// 参考 SAT-2 规范、Linux drivers/ata/libata-scsi.c 与 smartmontools
// sat_device::ata_pass_through 的实现。
const (
	ataPassThrough16     = 0x85   // ATA PASS-THROUGH (16) opcode
	ataProtocolPIIDataIn = 4 << 1 // PROTOCOL=4: PIO data-in
	// T_LENGTH=2（传输长度取 SECTOR COUNT 字段）、BYT_BLOK=1、
	// T_DIR=1（设备到主机）、CK_COND=0（不要求返回寄存器）。
	ataTLengthSectors = 0x0e
	ataSmartReadData  = 0xd0 // SMART READ DATA
	ataSmartCmd       = 0xb0 // SMART

	// SMART 命令在 LBA MID/HIGH 中的寄存器签名，见 include/linux/ata.h
	// 的 ATA_SMART_LBAM_PASS / ATA_SMART_LBAH_PASS。
	ataSmartLBAMid  = 0x4f
	ataSmartLBAHigh = 0xc2
)

// ataSmartReadDataCDB 构造读取 512 字节 SMART 属性数据的 ATA PASS-THROUGH(16)
// CDB：SMART READ DATA，LBA LOW=0、LBA MID=0x4F、LBA HIGH=0xC2。
func ataSmartReadDataCDB() [16]byte {
	var cdb [16]byte
	cdb[0] = ataPassThrough16
	cdb[1] = ataProtocolPIIDataIn
	cdb[2] = ataTLengthSectors
	cdb[4] = ataSmartReadData // FEATURES 低字节
	cdb[6] = 1                // SECTOR COUNT 低字节：1 个扇区（512 字节）
	cdb[10] = ataSmartLBAMid  // LBA MID 低字节
	cdb[12] = ataSmartLBAHigh // LBA HIGH 低字节
	cdb[14] = ataSmartCmd
	return cdb
}
