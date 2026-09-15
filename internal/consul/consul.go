// Package consul 为配置源及注册发现驱动提供 env 驱动的进程级单例。
package consul

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

const probeTimeout = 10 * time.Second

// Client 是配置、发现和注册适配器共享的并发安全 Consul API 客户端。
type Client = *api.Client

// disableConsul 是显式禁用 Consul 组装的环境变量名。
const disableConsul = "DISABLE_CONSUL"

// newClient 仅在首次访问时读取 env；客户端及连接池随进程退出回收。
func newClient() (Client, bool, error) {
	if env.GetEnvAsBool(disableConsul) || (env.IsLocal() && env.GetEnv(api.HTTPAddrEnvName) == "") {
		log.WithModule("consul").Warn("Consul client is disabled")
		return nil, true, nil
	}
	config := api.DefaultConfig()
	if strings.TrimSpace(config.Address) == "" {
		return nil, false, errors.New("consul address is empty")
	}
	// 将域名交给 SDK 和 transport，不在初始化时固定其解析结果。
	log.WithModule("consul").With("address", config.Address).Info("Initializing Consul client and checking the cluster leader")
	client, err := api.NewClient(config)
	if err != nil {
		return nil, false, fmt.Errorf("create Consul client: %w", err)
	}
	// 探测只约束首次可用性；结果（包括错误）由单例缓存，不创建重试任务。
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	leader, err := client.Status().LeaderWithQueryOptions((&api.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return nil, false, fmt.Errorf("check Consul leader: %w", err)
	}
	if leader == "" {
		return nil, false, errors.New("check Consul leader: no elected leader")
	}
	return client, false, nil
}
