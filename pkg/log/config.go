package log

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
)

// OutputConfig 描述单个日志输出端的启用、级别和字段过滤策略。
type OutputConfig struct {
	Disable    bool
	Level      kratoslog.Level
	FilterKeys []string
}

// RotatingConfig 描述文件轮转策略。
type RotatingConfig struct {
	Disable    bool
	MaxSize    int
	MaxFileAge int
	MaxFiles   int
	LocalTime  bool
	Compress   bool
}

// FileConfig 描述文件输出端及其轮转策略。
type FileConfig struct {
	OutputConfig
	Path     string
	Rotating RotatingConfig
}

// Config 描述可动态更新的日志配置。
type Config struct {
	Level       kratoslog.Level
	FilterEmpty bool
	FilterKeys  []string
	TimeFormat  string
	Std         OutputConfig
	File        FileConfig
}

// UpdateLogger 提供动态更新日志配置的能力。
type UpdateLogger interface {
	Update(Config) error
}

// NewUpdateLogger 返回仅暴露配置更新能力的 SharedState 视图。
func NewUpdateLogger(shared *SharedState) UpdateLogger {
	return shared
}
