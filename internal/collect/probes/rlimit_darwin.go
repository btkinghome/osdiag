//go:build darwin

package probes

// Darwin 的 syscall 包未导出 RLIMIT_NPROC（值为 7）。
const sysRLIMITNPROC = 7
