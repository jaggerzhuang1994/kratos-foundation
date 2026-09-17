package deadline

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Config 是截止时间策略对应的 protobuf 配置类型。
type Config = *config_pb.Middleware_Deadline

type compiledPolicy struct {
	// defaults 保存未命中路由时使用的默认策略。
	defaults policy
	// paths 按完整操作名保存精确匹配策略，优先于前缀规则。
	paths map[string]policy
	// prefixes 保存最长前缀匹配索引；无前缀规则时为 nil。
	prefixes *prefixIndex
}

type routePolicy struct {
	// prefix 指定待编译的路由前缀。
	prefix string
	// policy 保存该路由继承默认值后的完整策略。
	policy policy
}

// Store 编译并发布按路由选择的 Deadline 策略，更新失败时保留旧配置。
type Store struct {
	// current 原子发布完整只读策略快照，单次请求只读取一个版本。
	current atomic.Pointer[compiledPolicy]
}

// NewStore 编译服务端和客户端共用的截止时间路由策略。
func NewStore(config Config) (*Store, error) {
	store := &Store{}
	if err := store.Update(config); err != nil {
		return nil, err
	}
	return store, nil
}

// Update 完整校验并原子替换策略；失败时保留上一个生效版本。
func (s *Store) Update(config Config) error {
	compiled, err := compilePolicy(config)
	if err != nil {
		return err
	}
	s.current.Store(compiled)
	return nil
}

func (s *Store) resolve(operation string) policy {
	compiled := s.current.Load()
	// 精确路径优先，前缀树选择最长匹配；两者来自同一份不可变快照。
	if len(compiled.paths) != 0 {
		if matched, ok := compiled.paths[operation]; ok {
			return matched
		}
	}
	if compiled.prefixes != nil {
		if selected, ok := compiled.prefixes.lookup(operation); ok {
			return selected
		}
	}
	return compiled.defaults
}

func compilePolicy(config Config) (*compiledPolicy, error) {
	// 缺失配置使用 10s；保留字段存在性，让显式 0s 仍可关闭回退超时。
	defaults := policy{fallbackTimeout: 10 * time.Second}
	var err error
	if config != nil {
		if defaults.fallbackTimeout, err = nonNegativeDuration(
			"fallback_timeout",
			config.FallbackTimeout,
			defaults.fallbackTimeout,
		); err != nil {
			return nil, err
		}
		if defaults.maxTimeout, err = nonNegativeDuration(
			"max_timeout",
			config.MaxTimeout,
			0,
		); err != nil {
			return nil, err
		}
		if defaults.minBudget, err = nonNegativeDuration(
			"min_budget",
			config.MinBudget,
			0,
		); err != nil {
			return nil, err
		}
	}
	if err := validateSettings("deadline", defaults); err != nil {
		return nil, err
	}

	compiled := &compiledPolicy{defaults: defaults}
	var prefixes []routePolicy
	seen := make(map[string]struct{}, len(config.GetRoutes()))
	for index, rule := range config.GetRoutes() {
		if rule == nil {
			return nil, fmt.Errorf("deadline.routes[%d] is nil", index)
		}
		next, err := applyRouteSettings(defaults, rule)
		if err != nil {
			return nil, fmt.Errorf("deadline.routes[%d]: %w", index, err)
		}
		path, prefix := rule.GetPath(), rule.GetPrefix()
		if (path == "") == (prefix == "") {
			return nil, fmt.Errorf(
				"deadline.routes[%d] requires exactly one of path or prefix",
				index,
			)
		}
		key := "path:" + path
		if prefix != "" {
			key = "prefix:" + prefix
		}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("deadline.routes[%d] duplicates %s", index, key)
		}
		seen[key] = struct{}{}
		if path != "" {
			if compiled.paths == nil {
				compiled.paths = make(map[string]policy)
			}
			compiled.paths[path] = next
		} else {
			prefixes = append(prefixes, routePolicy{prefix: prefix, policy: next})
		}
	}
	compiled.prefixes = newPrefixIndex(prefixes)
	return compiled, nil
}

func applyRouteSettings(
	base policy,
	rule *config_pb.Middleware_Deadline_RouteRule,
) (policy, error) {
	var err error
	if base.fallbackTimeout, err = nonNegativeDuration(
		"fallback_timeout",
		rule.FallbackTimeout,
		base.fallbackTimeout,
	); err != nil {
		return policy{}, err
	}
	if base.maxTimeout, err = nonNegativeDuration(
		"max_timeout",
		rule.MaxTimeout,
		base.maxTimeout,
	); err != nil {
		return policy{}, err
	}
	if base.minBudget, err = nonNegativeDuration(
		"min_budget",
		rule.MinBudget,
		base.minBudget,
	); err != nil {
		return policy{}, err
	}
	if err := validateSettings("route", base); err != nil {
		return policy{}, err
	}
	return base, nil
}

func nonNegativeDuration(
	name string,
	value *durationpb.Duration,
	fallback time.Duration,
) (time.Duration, error) {
	if value == nil {
		return fallback, nil
	}
	if err := value.CheckValid(); err != nil {
		return 0, fmt.Errorf("%s is invalid: %w", name, err)
	}
	if duration := value.AsDuration(); duration >= 0 {
		return duration, nil
	}
	return 0, fmt.Errorf("%s cannot be negative", name)
}

func validateSettings(name string, value policy) error {
	if err := value.validate(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if value.fallbackTimeout > 0 && value.minBudget > value.fallbackTimeout {
		return fmt.Errorf(
			"%s min_budget (%s) cannot exceed fallback_timeout (%s)",
			name,
			value.minBudget,
			value.fallbackTimeout,
		)
	}
	if value.maxTimeout > 0 && value.minBudget > value.maxTimeout {
		return fmt.Errorf(
			"%s min_budget (%s) cannot exceed max_timeout (%s)",
			name,
			value.minBudget,
			value.maxTimeout,
		)
	}
	return nil
}
