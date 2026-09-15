package consul

import (
	"slices"
	"testing"
)

func TestDefaultRemoteConfigPaths(t *testing.T) {
	provider := NewRemoteConfigPathsProvider()
	want := []string{
		"configs/common*.yaml", "configs/prod/common*.yaml",
		"secrets/common*.yaml", "secrets/prod/common*.yaml",
		"configs/auth_service/*.yaml", "configs/auth_service/prod/*.yaml",
		"secrets/auth_service/*.yaml", "secrets/auth_service/prod/*.yaml",
	}
	got := provider("auth_service", "prod")
	if !slices.Equal(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	// 调用方可修改返回列表，不影响后续配置组装。
	got[0] = "changed"
	if next := provider("auth_service", "prod"); !slices.Equal(next, want) {
		t.Fatalf("paths shared between calls: %v", next)
	}
}
