package log

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// caller 仅跳过已知的日志转发帧，保留业务辅助函数、中间件及 SDK 的事件来源。
// 扫描上限约束异常深栈的开销；不能定位时明确返回 unknown，不猜测业务调用位置。
// 先只展开最近 callerScanShort 帧，通常已能定位；不足且栈更深时再按上限完整重扫，结果与完整扫描一致。
func caller(depth int) Valuer {
	return func(context.Context) any {
		var pcs [callerScanLimit]uintptr
		n := runtime.Callers(2, pcs[:callerScanShort])
		if location, found := locateCaller(pcs[:n], depth); found || n < callerScanShort {
			return location
		}
		n = runtime.Callers(2, pcs[:])
		location, _ := locateCaller(pcs[:n], depth)
		return location
	}
}

const (
	callerScanShort = 16
	callerScanLimit = 64
)

// locateCaller 跳过转发帧后返回第 depth 个应用帧的位置；遇到无源码或 runtime 帧时返回 unknown。
// found 为 false 表示这些地址已经扫完仍未定位，需要更深的栈才能判断。
func locateCaller(pcs []uintptr, depth int) (location any, found bool) {
	remaining := depth
	for _, pc := range pcs {
		for _, frame := range framesForPC(pc) {
			if frame.wrapper {
				continue
			}
			if frame.unknown {
				return unknownCaller, true
			}
			remaining--
			if remaining == 0 {
				return frame.location, true
			}
		}
	}
	return unknownCaller, false
}

// unknownCaller 是无法定位调用点时的字段值，预先装箱避免分配。
var unknownCaller any = "unknown"

// callerFrame 是单个调用地址（含内联展开）的帧摘要。
type callerFrame struct {
	// wrapper 表示已知的日志转发帧，定位时跳过。
	wrapper bool
	// unknown 表示无源码文件或 runtime 帧，定位到此即返回 unknown。
	unknown bool
	// location 为预先装箱的 "目录/文件.go:行号"，写入日志字段时无需再分配。
	location any
}

// callerFrames 缓存调用地址到帧摘要的映射。代码地址在进程内不变，缓存无需失效；
// 条目数以程序中实际出现的日志调用点为界。
var callerFrames sync.Map

// framesForPC 返回 pc 展开后的帧摘要；并发首次解析的结果相同，重复写入不影响正确性。
func framesForPC(pc uintptr) []callerFrame {
	if cached, ok := callerFrames.Load(pc); ok {
		return cached.([]callerFrame)
	}
	var result []callerFrame
	frames := runtime.CallersFrames([]uintptr{pc})
	for {
		frame, more := frames.Next()
		file := strings.ReplaceAll(frame.File, "\\", "/")
		if slash := strings.LastIndexByte(file, '/'); slash >= 0 {
			if parent := strings.LastIndexByte(file[:slash], '/'); parent >= 0 {
				file = file[parent+1:]
			}
		}
		result = append(result, callerFrame{
			wrapper:  callerWrapper(frame.Function),
			unknown:  frame.File == "" || strings.HasPrefix(frame.Function, "runtime."),
			location: file + ":" + strconv.Itoa(frame.Line),
		})
		if !more {
			break
		}
	}
	callerFrames.Store(pc, result)
	return result
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
