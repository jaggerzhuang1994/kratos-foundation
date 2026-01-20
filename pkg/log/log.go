package log

import (
	"context"

	"github.com/go-kratos/kratos/v2/log"
)

const moduleKey = "module"

type ModuleConfig interface {
	GetDisable() bool
	GetLevel() string
	GetFilterKeys() []string
}

type Log interface {
	log.Logger

	With(...any) Log
	WithModule(string, ...ModuleConfig) Log
	WithContext(context.Context) Log
	WithCallerDepth(int) Log
	// AddCallerDepth 常用于中间套一层 logger 的场景
	AddCallerDepth(...int) Log
	WithFilterKeys(...string) Log

	Debug(a ...any)
	Debugf(format string, a ...any)
	Debugw(keyvals ...any)
	Info(a ...any)
	Infof(format string, a ...any)
	Infow(keyvals ...any)
	Warn(a ...any)
	Warnf(format string, a ...any)
	Warnw(keyvals ...any)
	Error(a ...any)
	Errorf(format string, a ...any)
	Errorw(keyvals ...any)
	Fatal(a ...any)
	Fatalf(format string, a ...any)
	Fatalw(keyvals ...any)
}

type helper struct {
	*log.Helper
}

func NewLog(logger Logger) Log {
	return &helper{
		log.NewHelper(logger),
	}
}

func (l *helper) Log(level log.Level, keyvals ...any) error {
	return l.Helper.Logger().Log(level, keyvals...)
}

func (l *helper) getLogger() Logger {
	return l.Helper.Logger().(Logger)
}

func (l *helper) With(kv ...any) Log {
	return &helper{
		log.NewHelper(l.getLogger().With(kv...)),
	}
}

func (l *helper) WithModule(module string, optionalModuleLog ...ModuleConfig) Log {
	if len(optionalModuleLog) == 0 {
		return l.With(moduleKey, module)
	}

	moduleConfig := optionalModuleLog[0]
	if moduleConfig.GetDisable() {
		return &helper{
			log.NewHelper(l.getLogger().Disable()),
		}
	}

	if moduleConfig.GetLevel() != "" {
		level := log.ParseLevel(moduleConfig.GetLevel())
		return &helper{
			log.NewHelper(
				l.getLogger().With(moduleKey, module).FilterLevel(level).FilterKeys(moduleConfig.GetFilterKeys()...),
			),
		}
	}

	return &helper{
		log.NewHelper(
			l.getLogger().With(moduleKey, module).FilterKeys(moduleConfig.GetFilterKeys()...),
		),
	}
}

func (l *helper) WithContext(ctx context.Context) Log {
	return &helper{
		log.NewHelper(
			l.getLogger().WithContext(ctx),
		),
	}
}

func (l *helper) WithCallerDepth(callerDepth int) Log {
	return &helper{
		log.NewHelper(
			l.getLogger().WithCallerDepth(callerDepth),
		),
	}
}

func (l *helper) AddCallerDepth(optionalCallerDepth ...int) Log {
	return &helper{
		log.NewHelper(
			l.getLogger().AddCallerDepth(optionalCallerDepth...),
		),
	}
}

func (l *helper) WithFilterKeys(keys ...string) Log {
	return &helper{
		log.NewHelper(
			l.getLogger().FilterKeys(keys...),
		),
	}
}
