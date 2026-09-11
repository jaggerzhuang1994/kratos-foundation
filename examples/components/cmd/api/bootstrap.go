package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/go-kratos/kratos/v2/registry"
	fileconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/file"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

type configPath string

func newSources(logger log.Logger, path configPath) (config.Sources, error) {
	// 模板要求一个存在的文件，避免文件源未匹配时仅告警并使用默认配置启动。
	info, err := os.Stat(string(path))
	if err != nil {
		return nil, fmt.Errorf("stat configuration: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("configuration must be a regular file")
	}
	sources, err := fileconfig.NewSources(logger, fileconfig.PathList{string(path)})
	return config.Sources(sources), err
}

func newRegistrar() registry.Registrar { return nil }

func newDiscovery() registry.Discovery { return nil }

func boot(_ bootstrap.InfrastructureBootstrap, spec *bootstrap.Spec, service *demoService, messages *messaging, redisManager redis.Manager) (bootstrap.Bootstrap, error) {
	spec.Http().Register(func(srv server.HTTPServer) error { service.register(srv); return nil })
	spec.Http().HealthChecks(server.ReadinessCheck{Name: "redis", Check: func(ctx context.Context) error { return redisManager.Default().Ping(ctx).Err() }})
	// 任务只读 Redis，沿用调度器的超时和释放顺序，不创建额外后台状态。
	for _, name := range []string{"redis-heartbeat", "cache-size", "cache-ttl"} {
		spec.Job().RegisterCron(name, "@every 10s", job.TaskFunc(func(ctx context.Context) error {
			return runRedisJob(ctx, redisManager, name)
		}))
	}

	if err := messages.Register(bootstrap.ApplicationSpec(spec)); err != nil {
		return bootstrap.Bootstrap{}, err
	}
	return bootstrap.Bootstrap{}, nil
}

func runRedisJob(ctx context.Context, manager redis.Manager, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var value any
	var err error
	switch name {
	case "redis-heartbeat":
		value, err = manager.Default().Ping(ctx).Result()
	case "cache-size":
		value, err = manager.Default().DBSize(ctx).Result()
	case "cache-ttl":
		// 缺键的 TTL=-2 是有效观测结果；仅查询专属探针键，不删除任何数据。
		value, err = manager.Default().TTL(ctx, "components:maintenance:probe").Result()
	default:
		return fmt.Errorf("unknown redis job %q", name)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	slog.InfoContext(ctx, "runRedisJob | sampled", "job_name", name, "value", value)
	return nil
}
