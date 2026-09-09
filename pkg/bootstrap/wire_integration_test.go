package bootstrap_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 在临时模块生成并运行 Wire 组装，检查当前构造函数和 cleanup，避免维护生成副本。
func TestWireAssemblyGeneratesAndRunsCleanup(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	modulePath := "github.com/jaggerzhuang1994/kratos-foundation/v2"
	for _, name := range []string{"go.mod", "go.sum"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "go.mod" {
			content := strings.Replace(string(data), "module "+modulePath, "module wireassembly", 1)
			content += "\nrequire " + modulePath + " v2.0.0\nreplace " + modulePath + " => " + strconv.Quote(root) + "\n"
			data = []byte(content)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"wire.go", "assembly.go", "assembly_test.go", "business.go", "business_test.go"} {
		data, err := os.ReadFile(filepath.Join("testdata", "wireassembly", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, args := range [][]string{{"tool", "wire", "."}, {"test", "-race", "-count=1", "-timeout=60s", "-v", "."}} {
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "APP_ENV=local")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
		}
		if args[0] == "test" {
			t.Logf("business fixture results:\n%s", output)
		}
		if args[0] == "tool" {
			generated, err := os.ReadFile(filepath.Join(dir, "wire_gen.go"))
			if err != nil {
				t.Fatal(err)
			}
			// 检查真实依赖图形成阶段链，不约束各阶段内部 provider 的排序。
			previous := -1
			for _, call := range []string{"bootstrap.NewInfrastructureBootstrap(", "newBootstrap(", "bootstrap.NewBootstrap(", "bootstrap.NewKratosApp("} {
				position := strings.Index(string(generated), call)
				if position <= previous {
					t.Fatalf("generated stage %s is missing or out of order", call)
				}
				previous = position
			}
		}
	}
}
