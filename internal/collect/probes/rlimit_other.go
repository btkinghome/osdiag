//go:build !linux && !darwin

package probes

// 其他类 Unix 平台的 RLIMIT_NPROC 通常为 7；非 Unix 平台不支持本探测。
const sysRLIMITNPROC = 7
