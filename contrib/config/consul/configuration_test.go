package consul

import "testing"

func TestAddConfigSourceSkipsEmptyAndDisabled(t *testing.T) {
	if sources, err := AddConfigSource()(); err != nil || len(sources) != 0 {
		t.Fatalf("empty: %v", err)
	}
	loader := AddConfigSource("configs/app.yaml")
	// 声明阶段不初始化单例，执行阶段读取此环境值。
	t.Setenv("DISABLE_CONSUL", "true")
	if sources, err := loader(); err != nil || len(sources) != 0 {
		t.Fatalf("disabled: %v", err)
	}
}
