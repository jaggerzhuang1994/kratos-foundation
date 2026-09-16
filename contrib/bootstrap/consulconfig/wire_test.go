package consulconfig

import (
	"slices"
	"testing"
)

func TestDefaultRemoteConfigPaths(t *testing.T) {
	want := []string{
		"configs/common*.yaml", "configs/prod/common*.yaml",
		"secrets/common*.yaml", "secrets/prod/common*.yaml",
		"configs/auth_service/*.yaml", "configs/auth_service/prod/*.yaml",
		"secrets/auth_service/*.yaml", "secrets/auth_service/prod/*.yaml",
	}
	got := RemoteConfigPaths("auth_service", "prod")
	if !slices.Equal(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	if paths := RemoteConfigPaths("auth_service", "prod"); !slices.Equal(paths, got) {
		t.Fatalf("provider paths = %v, want %v", paths, got)
	}
	// 调用方可修改返回列表，不影响后续配置组装。
	got[0] = "changed"
	if next := RemoteConfigPaths("auth_service", "prod"); !slices.Equal(next, want) {
		t.Fatalf("paths shared between calls: %v", next)
	}
}
