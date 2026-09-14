package app

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"google.golang.org/protobuf/types/known/durationpb"
)

type stopTimeoutSnapshot struct {
	version uint64
	timeout time.Duration
}

// StopPolicy 读取热更新超时，并保留最近一次校验通过的预算。
type StopPolicy struct {
	value        *foundationconfig.HotReloadValue[durationpb.Duration]
	currentValue atomic.Pointer[stopTimeoutSnapshot]
	stopDelay    time.Duration
	logger       stopPolicyLogger
}

// stopPolicyLogger 保留消息方法，让 Foundation 实例按当前 msgKey 输出，同时兼容外部 Kratos Logger。
type stopPolicyLogger interface {
	Infof(string, ...any)
	Errorf(string, ...any)
}

// NewStopPolicy 通过点分路径订阅停机超时，cleanup 取消订阅。
func NewStopPolicy(config Config, configManager foundationconfig.Manager, logger kratoslog.Logger, stopDelay time.Duration) (*StopPolicy, func(), error) {
	if err := validateStopTimeout(config.GetStopTimeout().AsDuration(), stopDelay); err != nil {
		return nil, nil, err
	}
	value, cancel, err := foundationconfig.NewHotReloadValue[durationpb.Duration](configManager, "app.stop_timeout", defaultConfig.GetStopTimeout())
	if err != nil {
		return nil, nil, err
	}
	initial, version := value.GetCurrent()
	if err := initial.CheckValid(); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("app stop_timeout: %w", err)
	}
	if err := validateStopTimeout(initial.AsDuration(), stopDelay); err != nil {
		cancel()
		return nil, nil, err
	}
	var policyLogger stopPolicyLogger
	if base, ok := logger.(foundationlog.Logger); ok {
		policyLogger = base.WithModule("app").With("function", "StopPolicy.current")
	} else {
		policyLogger = kratoslog.NewHelper(kratoslog.With(logger, "module", "app", "function", "StopPolicy.current"))
	}
	policy := &StopPolicy{value: value, stopDelay: stopDelay, logger: policyLogger}
	policy.currentValue.Store(&stopTimeoutSnapshot{version: version, timeout: initial.AsDuration()})
	var once sync.Once
	return policy, func() { once.Do(cancel) }, nil
}

// validateStopTimeout 保证应用总停机时间严格大于服务预停等待。
func validateStopTimeout(timeout, stopDelay time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("app stop_timeout must be positive")
	}
	if timeout <= stopDelay {
		return fmt.Errorf(
			"app stop_timeout (%s) must be greater than server stop_delay (%s)",
			timeout,
			stopDelay,
		)
	}
	return nil
}

// current 在读取时校验新版本；停机流程获取该值后自行冻结本次预算。
func (p *StopPolicy) current() time.Duration {
	for {
		old := p.currentValue.Load()
		value, version := p.value.GetCurrent()
		if old.version >= version {
			return old.timeout
		}
		err := value.CheckValid()
		if err == nil {
			err = validateStopTimeout(value.AsDuration(), p.stopDelay)
		}
		next := &stopTimeoutSnapshot{version: version, timeout: old.timeout}
		if err == nil {
			next.timeout = value.AsDuration()
		}
		// 记录已处理版本，即使非法也不重复校验和记录日志；CAS 避免旧版本覆盖新预算。
		if !p.currentValue.CompareAndSwap(old, next) {
			continue
		}
		if err != nil {
			p.logger.Errorf("Rejected application shutdown timeout update at version %d: %v", version, err)
		} else if old.timeout != next.timeout {
			p.logger.Infof("Updated application shutdown timeout at version %d to %s", version, next.timeout)
		}
		return next.timeout
	}
}
