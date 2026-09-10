package log

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

// outputConfig 描述单个日志输出端的启用、级别和字段过滤策略。
type outputConfig struct {
	Disable    bool
	Level      kratoslog.Level
	FilterKeys []string
}

// rotatingConfig 描述文件轮转策略。
type rotatingConfig struct {
	Disable    bool
	MaxSize    int
	MaxFileAge int
	MaxFiles   int
	LocalTime  bool
	Compress   bool
}

// fileConfig 描述文件输出端及其轮转策略。
type fileConfig struct {
	outputConfig
	Path     string
	Rotating rotatingConfig
}

// envConfig 保存从 LOG_* 环境变量解析的实例启动配置。
type envConfig struct {
	Level       kratoslog.Level
	FilterEmpty bool
	FilterKeys  []string
	TimeFormat  string
	Std         outputConfig
	File        fileConfig
}

const (
	// EnvLevel 配置根 Logger 的最低级别。
	EnvLevel = "LOG_LEVEL"
	// EnvFilterEmpty 控制是否过滤空值字段。
	EnvFilterEmpty = "LOG_FILTER_EMPTY"
	// EnvFilterKeys 配置逗号分隔的根敏感字段列表。
	EnvFilterKeys = "LOG_FILTER_KEYS"
	// EnvTimeFormat 配置时间戳格式。
	EnvTimeFormat = "LOG_TIME_FORMAT"
	// EnvStdDisable 控制是否禁用标准输出端。
	EnvStdDisable = "LOG_STD_DISABLE"
	// EnvStdLevel 配置标准输出端最低级别。
	EnvStdLevel = "LOG_STD_LEVEL"
	// EnvStdFilterKeys 配置标准输出端敏感字段。
	EnvStdFilterKeys = "LOG_STD_FILTER_KEYS"
	// EnvFileDisable 控制是否禁用文件输出端。
	EnvFileDisable = "LOG_FILE_DISABLE"
	// EnvFileLevel 配置文件输出端最低级别。
	EnvFileLevel = "LOG_FILE_LEVEL"
	// EnvFileFilterKeys 配置文件输出端敏感字段。
	EnvFileFilterKeys = "LOG_FILE_FILTER_KEYS"
	// EnvFilePath 配置当前日志文件路径。
	EnvFilePath = "LOG_FILE_PATH"
	// EnvFileRotatingDisable 控制是否禁用文件轮转。
	EnvFileRotatingDisable = "LOG_FILE_ROTATING_DISABLE"
	// EnvFileRotatingMaxSize 配置触发轮转的文件大小，单位为 MB。
	EnvFileRotatingMaxSize = "LOG_FILE_ROTATING_MAX_SIZE"
	// EnvFileRotatingMaxFileAge 配置旧文件保留天数。
	EnvFileRotatingMaxFileAge = "LOG_FILE_ROTATING_MAX_FILE_AGE"
	// EnvFileRotatingMaxFiles 配置最多保留的旧文件数。
	EnvFileRotatingMaxFiles = "LOG_FILE_ROTATING_MAX_FILES"
	// EnvFileRotatingLocalTime 控制轮转文件名是否使用本地时间。
	EnvFileRotatingLocalTime = "LOG_FILE_ROTATING_LOCAL_TIME"
	// EnvFileRotatingCompress 控制是否压缩已轮转文件。
	EnvFileRotatingCompress = "LOG_FILE_ROTATING_COMPRESS"
)

// newEnvConfig 一次性读取并校验全部 LOG_* 环境变量。
func newEnvConfig() (envConfig, error) {
	level, err := envLevel(EnvLevel, kratoslog.LevelInfo)
	if err != nil {
		return envConfig{}, err
	}
	filterEmpty, err := envBool(EnvFilterEmpty, true)
	if err != nil {
		return envConfig{}, err
	}
	filterKeys := envCSV(EnvFilterKeys, nil)
	timeFormat := envString(EnvTimeFormat, time.RFC3339)

	stdDisable, err := envBool(EnvStdDisable, false)
	if err != nil {
		return envConfig{}, err
	}
	stdLevel, err := envLevel(EnvStdLevel, level)
	if err != nil {
		return envConfig{}, err
	}
	stdFilterKeys := envCSV(EnvStdFilterKeys, []string{
		ServiceIDKey,
		ServiceNameKey,
		ServiceVersionKey,
	})

	fileDisable, err := envBool(EnvFileDisable, false)
	if err != nil {
		return envConfig{}, err
	}
	fileLevel, err := envLevel(EnvFileLevel, level)
	if err != nil {
		return envConfig{}, err
	}
	filePath := envString(EnvFilePath, "./app.log")
	fileFilterKeys := envCSV(EnvFileFilterKeys, nil)
	rotatingDisable, err := envBool(EnvFileRotatingDisable, false)
	if err != nil {
		return envConfig{}, err
	}
	maxSize, err := envNonNegativeInt(EnvFileRotatingMaxSize, 100)
	if err != nil {
		return envConfig{}, err
	}
	maxFileAge, err := envNonNegativeInt(EnvFileRotatingMaxFileAge, 0)
	if err != nil {
		return envConfig{}, err
	}
	maxFiles, err := envNonNegativeInt(EnvFileRotatingMaxFiles, 0)
	if err != nil {
		return envConfig{}, err
	}
	localTime, err := envBool(EnvFileRotatingLocalTime, false)
	if err != nil {
		return envConfig{}, err
	}
	compress, err := envBool(EnvFileRotatingCompress, false)
	if err != nil {
		return envConfig{}, err
	}

	config := envConfig{
		Level:       level,
		FilterEmpty: filterEmpty,
		FilterKeys:  filterKeys,
		TimeFormat:  timeFormat,
		Std: outputConfig{
			Disable:    stdDisable,
			Level:      stdLevel,
			FilterKeys: stdFilterKeys,
		},
		File: fileConfig{
			outputConfig: outputConfig{
				Disable:    fileDisable,
				Level:      fileLevel,
				FilterKeys: fileFilterKeys,
			},
			Path: filePath,
			Rotating: rotatingConfig{
				Disable:    rotatingDisable,
				MaxSize:    maxSize,
				MaxFileAge: maxFileAge,
				MaxFiles:   maxFiles,
				LocalTime:  localTime,
				Compress:   compress,
			},
		},
	}
	if err := validateConfig(config); err != nil {
		switch {
		case strings.TrimSpace(config.TimeFormat) == "":
			return envConfig{}, fmt.Errorf("%s: %w", EnvTimeFormat, err)
		case !config.File.Disable && strings.TrimSpace(config.File.Path) == "":
			return envConfig{}, fmt.Errorf("%s: %w", EnvFilePath, err)
		case !config.File.Disable && !config.File.Rotating.Disable && config.File.Rotating.MaxSize == 0:
			return envConfig{}, fmt.Errorf("%s: %w", EnvFileRotatingMaxSize, err)
		}
		return envConfig{}, err
	}
	return config, nil
}

// envString 返回环境变量原值；变量不存在时返回默认值。
func envString(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	return value
}

// envBool 解析布尔环境变量；显式设置非法值时返回错误。
func envBool(key string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

// envNonNegativeInt 解析非负整数；零值保留给允许“不限制”的配置。
func envNonNegativeInt(key string, fallback int) (int, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("%s must not be negative", key)
	}
	return parsed, nil
}

// envLevel 严格解析日志级别，避免拼写错误静默回退。
func envLevel(key string, fallback kratoslog.Level) (kratoslog.Level, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	parsed, valid := parseLevel(value)
	if !valid {
		return 0, fmt.Errorf("%s must be one of debug, info, warn, error, fatal", key)
	}
	return parsed, nil
}

// parseLevel 解析支持的日志级别，忽略大小写和首尾空白。
func parseLevel(value string) (kratoslog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return kratoslog.LevelDebug, true
	case "info":
		return kratoslog.LevelInfo, true
	case "warn":
		return kratoslog.LevelWarn, true
	case "error":
		return kratoslog.LevelError, true
	case "fatal":
		return kratoslog.LevelFatal, true
	default:
		return 0, false
	}
}

// envCSV 解析逗号分隔列表，并去除空项和重复项。
func envCSV(key string, fallback []string) []string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return append([]string(nil), fallback...)
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}
