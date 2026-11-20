//go:build linux

package probes

// Linux 的 RLIMIT_NPROC 为 6；Go 的 syscall 包未导出该常量。
const sysRLIMITNPROC = 6
