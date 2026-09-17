package job

import (
	"context"
	"os"
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

// newJobLog 根据任务日志开关选择真实或空日志。
func newJobLog(logger log.Logger, options managerOptions) moduleLog {
	return withModule(logger, "job", options).With("job", nameValuer)
}

// cronLog 区分调度器自身日志与具体任务日志。
type cronLog log.Logger

// newCronLog 让调度器沿用同一任务名字段。
func newCronLog(logger log.Logger, options managerOptions) cronLog {
	return withModule(logger, "job/cron", options).With("job", nameValuer)
}

func withModule(logger log.Logger, module string, options managerOptions) log.Logger {
	if options.LoggingEnabled {
		return logger.WithModule(module)
	}
	return &disabledLogger{}
}

// disabledLogger 仅用于任务显式关闭生命周期日志，派生操作保持关闭状态。
type disabledLogger struct{}

func (l *disabledLogger) Log(log2.Level, ...any) error           { return nil }
func (l *disabledLogger) With(...any) log.Logger                 { return l }
func (l *disabledLogger) WithModule(string) log.Logger           { return l }
func (l *disabledLogger) WithContext(context.Context) log.Logger { return l }
func (l *disabledLogger) WithLevel(log2.Level) log.Logger        { return l }
func (l *disabledLogger) WithCallerDepth(int) log.Logger         { return l }
func (l *disabledLogger) WithFilterKeys(...string) log.Logger    { return l }

// 便捷入口统一经过丢弃输出，不拼接参数，避免关闭日志后仍触发业务格式化回调。
func (l *disabledLogger) Debug(...any)          { _ = l.Log(log2.LevelDebug) }
func (l *disabledLogger) Debugf(string, ...any) { _ = l.Log(log2.LevelDebug) }
func (l *disabledLogger) Debugw(...any)         { _ = l.Log(log2.LevelDebug) }
func (l *disabledLogger) Info(...any)           { _ = l.Log(log2.LevelInfo) }
func (l *disabledLogger) Infof(string, ...any)  { _ = l.Log(log2.LevelInfo) }
func (l *disabledLogger) Infow(...any)          { _ = l.Log(log2.LevelInfo) }
func (l *disabledLogger) Warn(...any)           { _ = l.Log(log2.LevelWarn) }
func (l *disabledLogger) Warnf(string, ...any)  { _ = l.Log(log2.LevelWarn) }
func (l *disabledLogger) Warnw(...any)          { _ = l.Log(log2.LevelWarn) }
func (l *disabledLogger) Error(...any)          { _ = l.Log(log2.LevelError) }
func (l *disabledLogger) Errorf(string, ...any) { _ = l.Log(log2.LevelError) }
func (l *disabledLogger) Errorw(...any)         { _ = l.Log(log2.LevelError) }

// Fatal 系列仍终止进程，任务日志开关只控制输出。
func (l *disabledLogger) Fatal(...any)          { os.Exit(1) }
func (l *disabledLogger) Fatalf(string, ...any) { os.Exit(1) }
func (l *disabledLogger) Fatalw(...any)         { os.Exit(1) }

// cronLoggerContract 只保留 robfig/cron 实际需要的日志能力。
type cronLoggerContract cron.Logger

type cronLogger struct {
	// cronLog 适配 robfig/cron 日志契约的日志入口。
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

// Info 降噪调度器内部日志。
func (logger *cronLogger) Info(msg string, keysAndValues ...any) {
	// 任务中间件已经记录完整生命周期；重复输出这些事件只会制造两套口径。
	if msg == "run" || msg == "schedule" || msg == "start" || msg == "stop" || msg == "wake" {
		return
	}
	logger.With(replaceKeysAndValues(keysAndValues)...).Info(msg)
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
