// Command api 演示仅依赖 Foundation 公共 API 的最小 HTTP 应用。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		// 组装或运行时监督失败后无法继续服务，交给进程管理器识别非零退出。
		panic(err)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("minimal-api", flag.ContinueOnError)
	path := flags.String("config", "examples/minimal/configs/config.yaml", "配置文件路径")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// 组装前仅记录入口与环境摘要，不输出 argv 或配置内容。
	log.WithModule("cmdapp").With("event", "command.starting", "command", flags.Name(),
		"version", version, "env", env.AppEnv(), "config_path", *path).Info("starting application")
	application, cleanup, err := initialize(configPath(*path), version)
	if err != nil {
		return fmt.Errorf("initialize application: %w", err)
	}
	defer cleanup()
	return application.Run()
}
