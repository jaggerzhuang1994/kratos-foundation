package testlog

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

// Config 仅用于跨包测试生成 LOG_* 环境变量，不属于日志公共契约。
type Config struct {
	Level       kratoslog.Level
	FilterEmpty bool
	FilterKeys  []string
	TimeFormat  string
	Std         OutputConfig
	File        FileConfig
}
