package testlog

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
)

// OutputConfig 描述单个日志输出端的测试配置。
type OutputConfig struct {
	// Disable 控制是否关闭该输出端；零值启用。
	Disable bool
	// Level 指定测试使用的最低日志级别。
	Level kratoslog.Level
	// FilterKeys 指定移除字段，支持末尾 * 前缀匹配；module 始终保留。
	FilterKeys []string
}

// RotatingConfig 描述文件轮转策略。
type RotatingConfig struct {
	// Disable 控制是否关闭文件轮转；不会关闭文件输出。
	Disable bool
	// MaxSize 指定轮转上限，单位 MB；文件输出和轮转均启用时必须为正数。
	MaxSize int
	// MaxFileAge 指定旧文件保留天数；0 表示不限制。
	MaxFileAge int
	// MaxFiles 指定旧文件保留数量；0 表示不限制。
	MaxFiles int
	// LocalTime 控制轮转文件名是否采用本地时间。
	LocalTime bool
	// Compress 控制是否压缩轮转后的旧文件。
	Compress bool
}

// FileConfig 描述文件输出端的测试配置。
type FileConfig struct {
	// OutputConfig 复用文件输出的启用、级别和字段过滤配置。
	OutputConfig
	// Path 指定测试日志文件路径。
	Path string
	// Rotating 指定测试使用的文件轮转策略。
	Rotating RotatingConfig
}

// Config 仅用于跨包测试生成 LOG_* 环境变量，不属于日志公共契约。
type Config struct {
	// Level 指定测试使用的最低日志级别。
	Level kratoslog.Level
	// FilterEmpty 控制是否移除 nil 或格式化后为空字符串的字段，0 和 false 不算空值。
	FilterEmpty bool
	// FilterKeys 指定移除字段，支持末尾 * 前缀匹配；module 始终保留。
	FilterKeys []string
	// TimeFormat 指定日志时间的 Go 格式模板。
	TimeFormat string
	// Std 指定标准输出端的测试配置。
	Std OutputConfig
	// File 指定文件输出端的测试配置。
	File FileConfig
}
