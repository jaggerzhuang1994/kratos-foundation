package main

import "runtime/debug"

// Version 是当前 protoc-gen-kratos-foundation-errors-v2 的构建版本。
var Version string

// init 在未通过编译参数注入版本时，从 Go 构建信息中补齐它。
func init() {
	if Version == "" {
		Version = GetVersion()
	}
}

// GetVersion 返回当前主模块的构建版本。
func GetVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return info.Main.Version
}
