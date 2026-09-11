package job

import (
	"context"
	"time"

	log2 "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/robfig/cron/v3"
)

// moduleLog 固定任务执行期间的日志模块，避免各中间件重复拼装字段。
type moduleLog log.Logger

// nameValuer 延迟从每次执行上下文读取任务名，避免创建派生日志器时捕获旧值。
var nameValuer = log2.Valuer(func(ctx context.Context) any {
	return JobNameFromContext(ctx)
})

// newJobLog 根据运行时开关选择真实或禁用日志，并显式返回模块配置错误。
func newJobLog(log log.Logger, options managerOptions) (moduleLog, error) {
	logger, err := withModule(log, "job", options)
	if err != nil {
		return nil, err
	}
	return logger.With("job", nameValuer), nil
}

// cronLog 区分调度器自身日志与具体任务日志。
type cronLog log.Logger

// newCronLog 让调度器沿用同一任务名字段，并显式返回模块配置错误。
func newCronLog(log log.Logger, options managerOptions) (cronLog, error) {
	logger, err := withModule(log, "job/cron", options)
	if err != nil {
		return nil, err
	}
	return logger.With("job", nameValuer), nil
}

// withModule 在关闭生命周期日志时仍保留同一 Logger 调用面，并传播配置错误。
func withModule(logger log.Logger, module string, options managerOptions) (log.Logger, error) {
	if options.LoggingEnabled {
		return logger.WithModule(module), nil
	}
	return logger.WithModuleConfig(module, disabledModuleLog{})
}

type disabledModuleLog struct{}

// GetDisable 声明该模块配置为关闭。
func (disabledModuleLog) GetDisable() bool { return true }

// GetLevel 返回空级别，让日志组件沿用默认级别。
func (disabledModuleLog) GetLevel() string { return "" }

// GetFilterKeys 表示禁用模块没有额外字段过滤规则。
func (disabledModuleLog) GetFilterKeys() []string { return nil }

// cronLoggerContract 只保留 robfig/cron 实际需要的日志能力。
type cronLoggerContract cron.Logger

type cronLogger struct {
	cronLog
}

// newCronLogger 将任务日志适配到 robfig/cron，同时保持统一字段。
func newCronLogger(
	log cronLog,
) cronLoggerContract {
	return &cronLogger{
		log.WithFilterKeys("now"),
	}
}

// Info 降噪调度器内部日志，并把唤醒事件降为调试级别。
func (logger *cronLogger) Info(msg string, keysAndValues ...any) {
	// 任务中间件已经记录完整生命周期；重复输出这些事件只会制造两套口径。
	if msg == "run" || msg == "schedule" || msg == "start" || msg == "stop" {
		return
	}
	if msg == "wake" {
		logger.With(replaceKeysAndValues(keysAndValues)...).Debug(msg)
	} else {
		logger.With(replaceKeysAndValues(keysAndValues)...).Info(msg)
	}
}

// Error 把 cron 错误和调度字段写入统一任务日志。
func (logger *cronLogger) Error(err error, msg string, keysAndValues ...any) {
	logger.With(replaceKeysAndValues(keysAndValues)...).With("error", err).Error(msg)
}

// replaceKeysAndValues 把 cron 时间字段格式化为稳定文本，同时避免修改调用方切片。
func replaceKeysAndValues(keysAndValues []any) []any {
	replaced := append([]any(nil), keysAndValues...)
	for i := 0; i < len(replaced); i += 2 {
		if key, ok := replaced[i].(string); ok && (key == "now" || key == "next") && i+1 < len(replaced) {
			if value, timeValue := replaced[i+1].(time.Time); timeValue {
				replaced[i+1] = value.Format(time.RFC3339)
			}
		}
	}
	return replaced
}
