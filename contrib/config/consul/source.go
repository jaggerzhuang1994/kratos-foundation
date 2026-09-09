// Package consul 提供按键路径组合 Consul KV 的配置源。
package consul

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	consulapi "github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	baseconsul "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// PathList 是 NewSources 按顺序加载的 Consul KV 路径列表。
type PathList []string

// Sources 是供 Wire 区分 Consul 配置源集合的独立类型。
type Sources []config.Source

// NewSources 返回按 paths 顺序排列的全部底层 Consul 配置源。
//
// 空路径列表或 nil 客户端表示禁用，返回 nil 且不报错。
// 结果借用客户端，由应用交给 config.NewManager 管理配置监听。
func NewSources(client baseconsul.Client, log log.Logger, paths PathList) (Sources, error) {
	if len(paths) == 0 {
		log.Warn("NewSources | sources.disabled | reason=empty paths")
		return nil, nil
	}
	if client == nil {
		log.Warn("NewSources | sources.disabled | reason=consul client not initialized")
		return nil, nil
	}
	if err := validatePaths(paths); err != nil {
		return nil, err
	}

	log.Infof("NewSources | sources.ready | paths=%v", paths)
	sources := make(Sources, 0, len(paths))
	for _, path := range paths {
		sources = append(sources, &kvSource{client: client, path: path})
	}
	return sources, nil
}

// validatePaths 拒绝空白或重复路径，因为它们分别会扫描意外根节点或重复建立监听。
func validatePaths(paths PathList) error {
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return errors.New("consul config source path is empty")
		}
		if strings.TrimSpace(path) != path {
			return fmt.Errorf(
				"consul config source path has surrounding whitespace: %q",
				path,
			)
		}
		if _, exists := seen[path]; exists {
			return fmt.Errorf("consul config source path is duplicated: %q", path)
		}
		seen[path] = struct{}{}
	}
	return nil
}

const loadTimeout = 10 * time.Second

type kvSource struct {
	client *consulapi.Client
	path   string
}

// Load 有界地读取完整前缀，暂时错误在同一请求预算内重试。
func (s *kvSource) Load() ([]*kratosconfig.KeyValue, error) {
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		values, _, err := s.query(ctx, 0, 0)
		if err == nil || !transientConsulError(err) {
			return values, err
		}
		if err := (reconnect.Backoff{}).Wait(ctx, attempt); err != nil {
			return nil, err
		}
	}
}

func (s *kvSource) Watch() (kratosconfig.Watcher, error) {
	ctx, cancel := context.WithCancel(context.Background())
	return &kvWatcher{source: s, ctx: ctx, cancel: cancel}, nil
}

func (s *kvSource) query(ctx context.Context, index uint64, wait time.Duration) ([]*kratosconfig.KeyValue, uint64, error) {
	options := (&consulapi.QueryOptions{WaitIndex: index, WaitTime: wait}).WithContext(ctx)
	pairs, meta, err := s.client.KV().List(s.path, options)
	if err != nil {
		return nil, 0, err
	}
	prefix := strings.TrimSuffix(s.path, "/") + "/"
	values := make([]*kratosconfig.KeyValue, 0, len(pairs))
	for _, pair := range pairs {
		// Consul List 按字面前缀查询，排除 settings-other 这样的相邻目录。
		if pair.Key != s.path && !strings.HasPrefix(pair.Key, prefix) {
			continue
		}
		key := strings.TrimPrefix(pair.Key, prefix)
		if key == "" {
			continue
		}
		values = append(values, &kratosconfig.KeyValue{
			Key: key, Value: append([]byte(nil), pair.Value...),
			Format: strings.TrimPrefix(filepath.Ext(key), "."),
		})
	}
	return values, meta.LastIndex, nil
}

func transientConsulError(err error) bool {
	var status consulapi.StatusError
	if errors.As(err, &status) {
		return status.Code == http.StatusTooManyRequests || status.Code >= 500 && status.Code < 600
	}
	return reconnect.Transient(err)
}
