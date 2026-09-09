package log

import (
	"fmt"
	"strings"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

func validateModule(module string) error {
	trimmed := strings.TrimSpace(module)
	if trimmed == "" {
		return fmt.Errorf("log module must not be empty")
	}
	if trimmed != module {
		return fmt.Errorf("log module %q contains surrounding whitespace", module)
	}
	return nil
}

func validateFilterKeys(filterKeys []string) error {
	seen := make(map[string]struct{}, len(filterKeys))
	for _, key := range filterKeys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			return fmt.Errorf("filter key must not be empty")
		}
		if trimmed != key {
			return fmt.Errorf("filter key %q contains surrounding whitespace", key)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("filter key %q is duplicated", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// validateConfig 校验环境变量和程序化构造共用的配置约束。
func validateConfig(config Config) error {
	levels := []struct {
		name  string
		level kratoslog.Level
	}{
		{name: "level", level: config.Level},
		{name: "std level", level: config.Std.Level},
		{name: "file level", level: config.File.Level},
	}
	for _, item := range levels {
		name, level := item.name, item.level
		if level < kratoslog.LevelDebug || level > kratoslog.LevelFatal {
			return fmt.Errorf("log %s is invalid", name)
		}
	}
	if strings.TrimSpace(config.TimeFormat) == "" {
		return fmt.Errorf("log time format must not be empty")
	}
	if config.File.Rotating.MaxSize < 0 ||
		config.File.Rotating.MaxFileAge < 0 ||
		config.File.Rotating.MaxFiles < 0 {
		return fmt.Errorf("log file rotating values must not be negative")
	}
	if !config.File.Disable && strings.TrimSpace(config.File.Path) == "" {
		return fmt.Errorf("log file path must not be empty when file logging is enabled")
	}
	if !config.File.Disable && !config.File.Rotating.Disable &&
		config.File.Rotating.MaxSize == 0 {
		return fmt.Errorf("log file rotating max size must be positive")
	}
	return nil
}
