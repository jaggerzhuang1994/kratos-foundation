// Command api 演示仅依赖 Foundation 公共 API 的包含常规组件业务的 HTTP 应用。
package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		// 组装或运行时监督失败后无法继续服务，交给进程管理器识别非零退出。
		panic(err)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("components-api", flag.ContinueOnError)
	path := flags.String("config", "examples/components/configs/config.yaml", "配置文件路径")
	if err := flags.Parse(args); err != nil {
		return err
	}
	application, cleanup, err := initialize(configPath(*path), version)
	if err != nil {
		return fmt.Errorf("initialize application: %w", err)
	}
	defer cleanup()
	return application.Run()
}
