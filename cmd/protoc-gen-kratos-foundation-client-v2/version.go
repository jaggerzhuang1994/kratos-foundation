package main

import (
	"fmt"
	"runtime/debug"

	"google.golang.org/protobuf/compiler/protogen"
)

// Version 是当前 protoc-gen-kratos-foundation-client-v2 的构建版本。
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

// protocVersion 把 protoc 的结构化版本转成可写入生成文件的字符串。
func protocVersion(gen *protogen.Plugin) string {
	version := gen.Request.GetCompilerVersion()
	if version == nil {
		return "(unknown)"
	}
	suffix := ""
	if version.GetSuffix() != "" {
		suffix = "-" + version.GetSuffix()
	}
	return fmt.Sprintf(
		"v%d.%d.%d%s",
		version.GetMajor(),
		version.GetMinor(),
		version.GetPatch(),
		suffix,
	)
}
