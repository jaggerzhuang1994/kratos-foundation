package output

import (
	"os"

	"github.com/go-kratos/kratos/v2/log"
)

type stdLogger struct {
	stdout log.Logger
	stderr log.Logger
}

// NewStd 创建将 error 及以上写到 stderr、其余级别写到 stdout 的 Logger。
func NewStd() log.Logger {
	return &stdLogger{
		stdout: log.NewStdLogger(os.Stdout),
		stderr: log.NewStdLogger(os.Stderr),
	}
}

// Log 根据级别选择标准输出流，并原样返回写入错误。
func (l *stdLogger) Log(level log.Level, keyvals ...any) error {
	if level >= log.LevelError {
		return l.stderr.Log(level, keyvals...)
	}
	return l.stdout.Log(level, keyvals...)
}
