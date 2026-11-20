//go:build !linux && !darwin

package app

import "errors"

func freeBytes(string) (uint64, error) { return 0, errors.New("当前平台不支持磁盘剩余空间检查") }
