// Package version 保存构建期注入的版本信息。
package version

var (
	// Version 由 -ldflags "-X luanbird/osdiag/internal/version.Version=..." 注入。
	Version = "0.1.0-m0"
	Commit  = ""
	Build   = "unknown"
)
