package app

import (
	"fmt"
	"sync/atomic"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"google.golang.org/protobuf/types/known/durationpb"
)

// StopPolicy 为应用提供经校验的热更新停机预算；App 在首次停止请求时冻结所读预算。
type StopPolicy struct {
	// timeout 原子保存最近一次校验通过的停机预算。
	timeout atomic.Int64
	// logger 记录预算更新及非法配置拒绝结果。
	logger stopPolicyLogger
}

// stopPolicyLogger 保留结构化事件方法，同时兼容 Foundation 和外部 Kratos Logger。
type stopPolicyLogger interface {
	Infow(...any)
	Warnw(...any)
}

// NewStopPolicy 以启动配置的 stop_timeout 为初值，并订阅 app.stop_timeout 的后续更新；cleanup 取消订阅。
// 更新在通知时校验：合法且变化时生效并记录 INFO，非法时保留原预算并记录 WARN。
func NewStopPolicy(config Config, configManager foundationconfig.Manager, logger kratoslog.Logger) (*StopPolicy, func(), error) {
	if err := checkStopTimeout(config.GetStopTimeout()); err != nil {
		return nil, nil, err
	}
	var policyLogger stopPolicyLogger
	if base, ok := logger.(foundationlog.Logger); ok {
		policyLogger = base.WithModule("app")
	} else {
		policyLogger = kratoslog.NewHelper(kratoslog.With(logger, "module", "app"))
	}
	policy := &StopPolicy{logger: policyLogger}
	policy.timeout.Store(int64(config.GetStopTimeout().AsDuration()))
	cancel, err := configManager.Subscribe("app.stop_timeout", new(durationpb.Duration), policy.update, defaultConfig.GetStopTimeout())
	if err != nil {
		return nil, nil, err
	}
	return policy, cancel, nil
}

// update 处理订阅通知；Manager 串行回调，读取方只通过原子值观察结果。
func (p *StopPolicy) update(_ string, value any, err error) {
	previous := time.Duration(p.timeout.Load())
	if err == nil {
		err = checkStopTimeout(value.(*durationpb.Duration))
	}
	if err != nil {
		p.logger.Warnw("event", "app.stop_policy.rejected", "stop_timeout", previous, "error", err, "msg", "stop timeout update rejected; keeping previous")
		return
	}
	next := value.(*durationpb.Duration).AsDuration()
	if next == previous {
		return
	}
	p.timeout.Store(int64(next))
	p.logger.Infow("event", "app.stop_policy.updated", "stop_timeout", next, "previous", previous, "msg", "stop timeout updated")
}

// current 返回最近一次校验通过的停机预算。
func (p *StopPolicy) current() time.Duration {
	return time.Duration(p.timeout.Load())
}

// checkStopTimeout 校验停机预算为合法且为正的 Duration；服务器预停等待由部署配置协调。
func checkStopTimeout(timeout *durationpb.Duration) error {
	if err := timeout.CheckValid(); err != nil {
		return fmt.Errorf("app stop_timeout is invalid: %w", err)
	}
	return validateStopTimeout(timeout.AsDuration())
}

// validateStopTimeout 仅校验应用自身预算。
func validateStopTimeout(timeout time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("app stop_timeout must be positive")
	}
	return nil
}
