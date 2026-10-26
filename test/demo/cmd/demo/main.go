//go:build demo

// Command demo 是本地 Kubernetes 监控演示用的多角色应用：同一镜像按 DEMO_ROLE 扮演
// 订单 API、库存 API、网关、定时任务、队列/Kafka 消费者、常驻进程、压测与一次性报表，
// 用框架的真实组件产生指标，供 observability/ 下的面板与告警验证。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

var version = "demo"

func main() {
	if err := run(os.Args[1:]); err != nil {
		panic(err)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("demo", flag.ContinueOnError)
	path := flags.String("config", "/app/configs/config.yaml", "配置文件路径")
	if err := flags.Parse(args); err != nil {
		return err
	}
	role := currentRole()
	if !validRole(role) {
		return fmt.Errorf("unknown DEMO_ROLE %q", role)
	}
	log.WithModule("cmdapp").With("event", "command.starting", "command", "demo", "role", role,
		"version", version, "env", env.AppEnv()).Info("starting application")
	application, cleanup, err := initialize(configPath(*path), version)
	if err != nil {
		return fmt.Errorf("initialize application: %w", err)
	}
	defer cleanup()
	return application.Run()
}
