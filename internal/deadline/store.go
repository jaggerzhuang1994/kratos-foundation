package deadline

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Config 是截止时间策略对应的 protobuf 配置类型。
type Config = *config_pb.Middleware_Deadline

type compiledPolicy struct {
	defaults policy
	routes   []routePolicy
}

type routePolicy struct {
	path   string
	prefix string
	policy policy
}

// Store 保存按路由选择的 Deadline 策略快照。
//
// Update 先完整编译新配置，再原子替换快照。Derive 的调用方无需加锁，且一次请求
// 只会取得一个完整版本。
type Store struct {
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
	selected := compiled.defaults
	longestPrefix := 0
	for _, route := range compiled.routes {
		if route.path == operation {
			return route.policy
		}
		if len(route.prefix) > longestPrefix && strings.HasPrefix(operation, route.prefix) {
			selected = route.policy
			longestPrefix = len(route.prefix)
		}
	}
	return selected
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

	compiled := &compiledPolicy{
		defaults: defaults,
		routes:   make([]routePolicy, 0, len(config.GetRoutes())),
	}
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
		compiled.routes = append(compiled.routes, routePolicy{
			path:   path,
			prefix: prefix,
			policy: next,
		})
	}
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
