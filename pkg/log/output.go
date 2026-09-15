package log

import (
	"os"
	"sync"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log/internal/output"
)

// preparedOutput 是准备完成的一代输出；发布后只读。
type preparedOutput struct {
	output      kratoslog.Logger
	file        kratoslog.Logger
	config      envConfig
	releaseFile func()
	ready       chan struct{}
}

// outputLogger 保持实例入口稳定，派生 Logger 不持有即将退役的文件句柄。
type outputLogger struct {
	*preparedOutput
	mu     sync.RWMutex
	closed bool
}

func prepareOutput(config envConfig, previous *preparedOutput) (*preparedOutput, bool, error) {
	next := &preparedOutput{config: config, ready: make(chan struct{})}
	reused := previous != nil && previous.config.File.Disable == config.File.Disable && previous.config.File.Path == config.File.Path && previous.config.File.Rotating == config.File.Rotating
	if reused {
		next.file, next.releaseFile = previous.file, previous.releaseFile
	} else if !config.File.Disable {
		fileConfig := output.FileConfig{Path: config.File.Path}
		if !config.File.Rotating.Disable {
			r := config.File.Rotating
			fileConfig.Rotating = &output.RotatingFileConfig{MaxSize: r.MaxSize, MaxFileAge: r.MaxFileAge, MaxFiles: r.MaxFiles, LocalTime: r.LocalTime, Compress: r.Compress}
		}
		file, release, err := output.NewFile(fileConfig)
		if err != nil {
			return nil, false, err
		}
		next.file, next.releaseFile = file, release
	}
	var sinks []kratoslog.Logger
	if next.file != nil {
		sinks = append(sinks, output.NewLevelFilter(output.NewFilter(next.file, false, output.FilterKeysSet(config.File.FilterKeys)), config.File.Level))
	}
	if !config.Std.Disable {
		sinks = append(sinks, output.NewLevelFilter(output.NewFilter(output.NewStd(), false, output.FilterKeysSet(config.Std.FilterKeys)), config.Std.Level))
	}
	next.output = output.NewStack(sinks...)
	return next, reused, nil
}

func newOutputLogger(config envConfig) (*outputLogger, func(), error) {
	prepared, _, err := prepareOutput(config, nil)
	if err != nil {
		return nil, nil, err
	}
	close(prepared.ready)
	out := &outputLogger{preparedOutput: prepared}
	return out, out.close, nil
}

func (l *outputLogger) close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	release := l.releaseFile
	ready := l.ready
	l.mu.Unlock()
	if ready != nil {
		<-ready
	}
	// 入场关闭并等到已有写入结束后，锁外等待文件轮转后台任务退出。
	if release != nil {
		release()
	}
}

// Log 与切换、关闭共享实例入场边界，释放后不再重新打开文件。
func (l *outputLogger) Log(level kratoslog.Level, keyvals ...any) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		return os.ErrClosed
	}
	// 等待前代资源退役，避免同路径轮转与旧文件写入并行。
	if l.ready != nil {
		<-l.ready
	}
	return l.output.Log(level, keyvals...)
}
