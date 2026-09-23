package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/file"
	_ "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/registry/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

type configPath string

func newSpec(application *app.Spec, servers *server.Spec, jobs *job.Spec, path configPath) (*bootstrap.Spec, error) {
	// 模板要求一个存在的文件，避免文件源未匹配时仅告警并使用默认配置启动。
	info, err := os.Stat(string(path))
	if err != nil {
		return nil, fmt.Errorf("stat configuration: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("configuration must be a regular file")
	}
	spec := bootstrap.NewSpec(application, servers, jobs, bootstrap.ConfigSources{}).Configuration(file.AddConfigSource(string(path)))
	return spec, nil
}

func boot(_ bootstrap.InfrastructureBootstrap, spec *bootstrap.Spec, service *demoService, messages *messaging, redisManager redis.Manager) (bootstrap.Bootstrap, error) {
	spec.Http().Register(func(srv server.HTTPServer) error { service.register(srv); return nil })
	spec.Health().Checks(server.ReadinessCheck{Name: "redis", Check: func(ctx context.Context) error { return redisManager.Default().Ping(ctx).Err() }})
	// 任务只读 Redis，沿用调度器的超时和释放顺序，不创建额外后台状态。
	for _, name := range []string{"redis-heartbeat", "cache-size", "cache-ttl"} {
		spec.Job().RegisterCron(name, "@every 10s", job.TaskFunc(func(ctx context.Context) error {
			return runRedisJob(ctx, redisManager, name, service.logger)
		}))
	}

	messages.Register(spec)
	return bootstrap.Bootstrap{}, nil
}

func runRedisJob(ctx context.Context, manager redis.Manager, name string, logger log.Logger) error {
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
	logger.WithModule("maintenance").WithContext(ctx).With("job_name", name, "value", value).Info("collected Redis maintenance data")
	return nil
}
