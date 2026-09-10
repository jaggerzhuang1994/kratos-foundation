package log

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 未装配 Wire Logger 的进程只使用标准输出，不能因全局日志打开 env 指定的文件。
func TestGlobalFallbackUsesSharedSettingsWithoutOpeningFiles(t *testing.T) {
	const mode = "KRATOS_LOG_FALLBACK_TEST"
	if os.Getenv(mode) == "1" {
		WithKV("fallback_field", "shared")
		Infof("fallback-message")
		return
	}
	path := filepath.Join(t.TempDir(), "must-not-open.log")
	command := exec.Command(os.Args[0], "-test.run=^TestGlobalFallbackUsesSharedSettingsWithoutOpeningFiles$")
	command.Env = append(os.Environ(), mode+"=1", EnvFilePath+"="+path, EnvFileDisable+"=false")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fallback process: %v: %s", err, output)
	}
	for _, want := range []string{"fallback_field=shared", "fallback-message", "caller=log/kratos_test.go:"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("fallback lacks %s: %s", want, output)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("fallback created file or stat failed: %v", err)
	}
}
