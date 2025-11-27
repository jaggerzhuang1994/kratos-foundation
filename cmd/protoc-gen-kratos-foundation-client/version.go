package main

import "runtime/debug"

var Version string

func init() {
	if Version == "" {
		Version = GetVersion()
	}
}

func GetVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return info.Main.Version
}
