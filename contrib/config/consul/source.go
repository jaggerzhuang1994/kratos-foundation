// Package consul 提供按键路径组合 Consul KV 的配置源。
package consul

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	consulapi "github.com/hashicorp/consul/api"
	baseconsul "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// PathList 是有序 Consul KV 路径列表；支持精确键、目录（直属 *.yaml）及完整 path.Match 模式。
type PathList []string

// Sources 是供 Wire 区分 Consul 配置源集合的独立类型。
type Sources []config.Source

// newSources 返回按 paths 顺序排列的全部底层 Consul 配置源。
//
// 构造事件使用全局日志，无需注入应用 Logger。
// 空路径列表或 nil 客户端表示禁用，返回 nil 且不报错。
// 结果借用客户端，由应用交给 config.NewManager 管理配置监听。
func newSources(client baseconsul.Client, paths PathList) (Sources, error) {
	logger := log.WithModule("config/consul")
	if len(paths) == 0 {
		logger.With("reason", "empty paths").Warn("Remote configuration is disabled")
		return nil, nil
	}
	if client == nil {
		logger.With("reason", "consul client not initialized").Warn("Remote configuration is disabled")
		return nil, nil
	}
	if err := validatePaths(paths); err != nil {
		return nil, err
	}

	logger.With("paths", paths).Info("Preparing Consul configuration sources")
	sources := make(Sources, 0, len(paths))
	for _, p := range paths {
		sources = append(sources, &kvSource{client: client, path: p})
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
		// 构造时校验完整模式，即使远端没有键也不能隐藏语法错误。
		if _, _, err := configPatternPrefix(path); err != nil {
			return err
		}
		if _, exists := seen[path]; exists {
			return fmt.Errorf("consul config source path is duplicated: %q", path)
		}
		seen[path] = struct{}{}
	}
	return nil
}

// configPatternPrefix 提取最长字面前缀；转义字符还原，首个通配符终止前缀。
func configPatternPrefix(pattern string) (string, bool, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return "", false, fmt.Errorf("invalid consul config pattern %q: %w", pattern, err)
	}
	var prefix strings.Builder
	for index := 0; index < len(pattern); index++ {
		switch pattern[index] {
		case '*', '?', '[':
			return prefix.String(), true, nil
		case '\\':
			// Match 已拒绝末尾孤立反斜杠；只移除转义符，其后 UTF-8 字节原样保留。
			index++
		}
		prefix.WriteByte(pattern[index])
	}
	return prefix.String(), false, nil
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
	queryPath, glob, err := configPatternPrefix(s.path)
	if err != nil {
		return nil, 0, err
	}
	pairs, meta, err := s.client.KV().List(queryPath, options)
	if err != nil {
		return nil, 0, err
	}
	// 显式排序保证初始加载与监听快照具有相同覆盖顺序。
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Key < pairs[j].Key })
	// 相对键只移除完整目录部分，保留文件名及扩展名。
	directory := queryPath[:strings.LastIndexByte(queryPath, '/')+1]
	// Consul 允许同名键与目录并存：每份快照优先精确键，尾部 / 强制目录。
	pattern := s.path
	if !glob {
		exact := false
		for _, pair := range pairs {
			if pair.Key == queryPath && !strings.HasSuffix(pair.Key, "/") {
				exact = true
				break
			}
		}
		if !exact {
			pattern = strings.TrimSuffix(s.path, "/") + "/*.yaml"
			directory = strings.TrimSuffix(queryPath, "/") + "/"
		}
	}
	values := make([]*kratosconfig.KeyValue, 0, len(pairs))
	for _, pair := range pairs {
		// 匹配完整键，目录简写也不能读取相邻前缀或更深层的文件。
		matched, err := path.Match(pattern, pair.Key)
		if err != nil {
			return nil, 0, fmt.Errorf("match consul config pattern: %w", err)
		}
		if !matched || strings.HasSuffix(pair.Key, "/") {
			continue
		}
		// 记录匹配后的完整 KV 键，避免相对键或输入 glob 隐藏实际来源；不输出配置值。
		log.WithModule("config/consul").With("function", "kvSource.query", "path", pair.Key).Debug("Loaded configuration file")
		key := strings.TrimPrefix(pair.Key, directory)

		values = append(values, &kratosconfig.KeyValue{
			Key: key, Value: append([]byte(nil), pair.Value...),
			Format: strings.TrimPrefix(path.Ext(key), "."),
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
