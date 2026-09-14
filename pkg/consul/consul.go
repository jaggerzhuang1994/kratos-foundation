// Package consul 负责组装、探测和清理进程共享的 Consul 客户端。
package consul

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

const probeTimeout = 10 * time.Second

// Client 是配置、发现和注册适配器共享的并发安全 Consul API 客户端。
type Client = *api.Client

// DisableConsul 是显式禁用 Consul 组装的环境变量名。
const DisableConsul = "DISABLE_CONSUL"

// Options 保存先于配置 Manager 解析的启用决策和 Consul SDK 配置。
type Options struct {
	// Disabled 为 true 时跳过构造与探测。
	Disabled bool
	// Config 及其 Address 在启用时必须提供；构造时复制其值，保留传输对象引用。
	Config *api.Config
}

// NewOptions 固定本包的启用决策；SDK 仍会在 NewClient 时补全零值配置。
func NewOptions() Options {
	return Options{
		Disabled: env.GetEnvAsBool(DisableConsul) ||
			(env.IsLocal() && env.GetEnv(api.HTTPAddrEnvName) == ""),
		Config: api.DefaultConfig(),
	}
}

// New 构造并探测共享 Consul 客户端，返回幂等的空闲连接清理函数。
//
// 启动诊断使用全局日志，无需先构造应用 Logger。
// Options.Disabled 为 true 时返回 nil 客户端和空清理函数。
func New(options Options) (Client, func(), error) {
	nopCleanup := func() {}

	if options.Disabled {
		log.WithModule("consul").With("function", "New").Warn("Consul client is disabled")
		return nil, nopCleanup, nil
	}
	if options.Config == nil {
		return nil, nopCleanup, errors.New("consul config is nil")
	}

	// SDK 默认补全会修改 Config 字段，使用副本保留调用方输入。
	config := *options.Config
	if strings.TrimSpace(config.Address) == "" {
		return nil, nopCleanup, errors.New("consul address is empty")
	}
	// 保留域名，由 HTTP transport 在每次新建连接时解析，允许 DNS 切换后恢复。

	log.WithModule("consul").With("function", "New", "address", config.Address).Info("Initializing Consul client and checking the cluster leader")

	client, err := api.NewClient(&config)
	// SDK 可能补全 Transport 或使用自定义 HttpClient，清理绑定实际使用的连接池。
	cleanup := nopCleanup
	if config.HttpClient != nil {
		cleanup = newConsulCleanup(config.HttpClient)
	} else if config.Transport != nil {
		cleanup = newConsulCleanup(config.Transport)
	}
	fail := func(err error) (Client, func(), error) {
		cleanup()
		return nil, nopCleanup, err
	}
	if err != nil {
		return fail(fmt.Errorf("create Consul client: %w", err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	leader, err := client.Status().LeaderWithQueryOptions(
		(&api.QueryOptions{}).WithContext(ctx),
	)
	if err != nil {
		return fail(fmt.Errorf("check Consul leader: %w", err))
	}
	if leader == "" {
		return fail(errors.New("check Consul leader: no elected leader"))
	}

	return client, cleanup, nil
}

type idleConnectionCloser interface {
	CloseIdleConnections()
}

// newConsulCleanup 保证共享 HTTP 传输的空闲连接只被关闭一次。
func newConsulCleanup(transport idleConnectionCloser) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			if transport != nil {
				transport.CloseIdleConnections()
			}
		})
	}
}
