package log

import (
	"context"
	"runtime"
	"strconv"
	"strings"
)

// caller 仅跳过已知的日志转发帧，保留业务辅助函数、中间件及 SDK 的事件来源。
// 扫描上限约束异常深栈的开销；不能定位时明确返回 unknown，不猜测业务调用位置。
func caller(depth int) Valuer {
	return func(context.Context) any {
		remaining := depth
		var pcs [64]uintptr
		n := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		for {
			frame, more := frames.Next()
			if !callerWrapper(frame.Function) {
				if frame.File == "" || strings.HasPrefix(frame.Function, "runtime.") {
					return "unknown"
				}
				remaining--
				if remaining > 0 {
					if !more {
						return "unknown"
					}
					continue
				}
				file := strings.ReplaceAll(frame.File, "\\", "/")
				if slash := strings.LastIndexByte(file, '/'); slash >= 0 {
					if parent := strings.LastIndexByte(file[:slash], '/'); parent >= 0 {
						file = file[parent+1:]
					}
				}
				return file + ":" + strconv.Itoa(frame.Line)
			}
			if !more {
				return "unknown"
			}
		}
	}
}

func callerWrapper(function string) bool {
	const foundation = "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/"
	if strings.HasPrefix(function, foundation+"log.(*logger).") {
		return true
	}
	switch function {
	case foundation + "log.globalLogger.Log",
		foundation + "log.kratosProxy.Log",
		foundation + "log/internal/output.(*moduleLogger).Log",
		foundation + "database.(*gormLoggerWriter).Printf",
		foundation + "kafka.(*loggerAdapter).Log",
		foundation + "job.(*cronLogger).Info",
		foundation + "job.(*cronLogger).Error",
		"github.com/twmb/franz-go/pkg/kgo.(*wrappedLogger).Log":
		return true
	}
	const kratos = "github.com/go-kratos/kratos/v2/log."
	name, ok := strings.CutPrefix(function, kratos)
	if !ok {
		name, ok = strings.CutPrefix(function, foundation+"log.")
	}
	if !ok {
		return false
	}
	if strings.HasPrefix(name, "(*Helper).") {
		return true
	}
	switch name {
	case "bindValues", "(*logger).Log", "(*loggerAppliance).Log",
		"Log", "Debug", "Debugf", "Debugw", "Info", "Infof", "Infow",
		"Warn", "Warnf", "Warnw", "Error", "Errorf", "Errorw", "Fatal", "Fatalf", "Fatalw":
		return true
	default:
		return false
	}
}
