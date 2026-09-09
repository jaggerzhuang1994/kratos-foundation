package output

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
)

type logCall struct {
	level   kratoslog.Level
	keyvals []any
}

type recordingLogger struct {
	calls []logCall
	err   error
}

func (l *recordingLogger) Log(level kratoslog.Level, keyvals ...any) error {
	l.calls = append(l.calls, logCall{
		level:   level,
		keyvals: append([]any(nil), keyvals...),
	})
	return l.err
}

type discardLogger struct{}

func (discardLogger) Log(kratoslog.Level, ...any) error { return nil }
