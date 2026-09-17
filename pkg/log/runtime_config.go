package log

import (
	"fmt"
	"slices"
	"strings"
)

// RuntimeConfig 描述运行期模块和输出策略，不包含固定日志字段。
// 使用普通 JSON 结构保留空列表的存在性。
type RuntimeConfig struct {
	// Level 覆盖根环境级别，并作为未显式配置输出端级别的默认值。
	Level *string `json:"level,omitempty"`
	// FilterKeys 根字段过滤规则；nil 继承环境配置，空切片清空本层；module 字段始终保留。
	FilterKeys []string `json:"filter_keys"`
	// Std 标准输出覆盖策略；nil 保留环境设置及根级别继承。
	Std *OutputPolicy `json:"std,omitempty"`
	// File 文件输出覆盖策略；nil 保留环境设置及根级别继承。
	File *FilePolicy `json:"file,omitempty"`
	// Modules 按顺序匹配的模块策略，仅首个命中项生效。
	Modules []ModulePolicy `json:"modules,omitempty"`
}

// ModulePolicy 描述单个模块匹配规则的日志策略。
type ModulePolicy struct {
	// Module 模块名或末尾带 * 的前缀模式；* 匹配全部模块。
	Module string `json:"module"`
	// Level 模块级别覆盖，优先于 WithLevel；nil 保留既有级别。
	Level *string `json:"level,omitempty"`
	// Disable 为 true 时禁用该模块；false 或 nil 均不能解除实例整体禁用。
	Disable *bool `json:"disable,omitempty"`
	// FilterKeys 追加到该模块的字段过滤规则，不清除其他层规则。
	FilterKeys []string `json:"filter_keys,omitempty"`
}

// matchModule 按声明顺序返回首个命中项，未设置字段不会从后续规则补齐。
func (c *RuntimeConfig) matchModule(module string) *ModulePolicy {
	if c == nil {
		return nil
	}
	if module == "" {
		module = "unknown"
	}
	for i := range c.Modules {
		rule := &c.Modules[i]
		prefix, wildcard := strings.CutSuffix(rule.Module, "*")
		if rule.Module == module || (wildcard && strings.HasPrefix(module, prefix)) {
			return rule
		}
	}
	return nil
}

// OutputPolicy 覆盖标准输出策略。
type OutputPolicy struct {
	// Level 标准输出级别；nil 先继承根 Level，再使用环境级别。
	Level *string `json:"level,omitempty"`
	// Disable 是否禁用标准输出；nil 继承启动环境。
	Disable *bool `json:"disable,omitempty"`
	// FilterKeys 标准输出字段过滤规则；nil 继承环境，空切片清空本层。
	FilterKeys []string `json:"filter_keys"`
}

// FilePolicy 覆盖文件输出策略。
type FilePolicy struct {
	// Enable 是否启用文件输出，可在启动后切换；nil 继承启动环境。
	Enable *bool `json:"enable,omitempty"`
	// Level 文件输出级别；nil 先继承根 Level，再使用环境级别。
	Level *string `json:"level,omitempty"`
	// FilterKeys 文件字段过滤规则；nil 继承环境，空切片清空本层。
	FilterKeys []string `json:"filter_keys"`
	// Path 日志文件路径覆盖；显式指定时不得为空。
	Path *string `json:"path,omitempty"`
	// Rotating 轮转参数覆盖；nil 继承启动环境。
	Rotating *RotatingPolicy `json:"rotating,omitempty"`
}

// RotatingPolicy 覆盖文件轮转参数。
type RotatingPolicy struct {
	// Disable 是否禁用轮转；nil 继承启动环境。
	Disable *bool `json:"disable,omitempty"`
	// MaxSize 轮转大小上限，单位 MB；nil 继承启动环境，启用文件及轮转时须大于 0。
	MaxSize *int `json:"max_size,omitempty"`
	// MaxFileAge 旧文件最长保留天数；0 不限制，nil 继承启动环境。
	MaxFileAge *int `json:"max_file_age,omitempty"`
	// MaxFiles 旧文件保留数量上限；0 不限制，nil 继承启动环境。
	MaxFiles *int `json:"max_files,omitempty"`
	// LocalTime 备份文件名是否使用本地时间；nil 继承启动环境。
	LocalTime *bool `json:"local_time,omitempty"`
	// Compress 是否压缩轮转文件；nil 继承启动环境。
	Compress *bool `json:"compress,omitempty"`
}

// ValidateRuntimeConfig 校验策略语法；与各实例 env 合并后的约束在发布前校验。
func ValidateRuntimeConfig(config *RuntimeConfig) error {
	if config == nil {
		return fmt.Errorf("log runtime config is nil")
	}
	if err := validatePolicy(config.Level, config.FilterKeys); err != nil {
		return err
	}
	if p := config.Std; p != nil {
		if err := validatePolicy(p.Level, p.FilterKeys); err != nil {
			return err
		}
	}
	if p := config.File; p != nil {
		if err := validatePolicy(p.Level, p.FilterKeys); err != nil {
			return err
		}
		if p.Path != nil && strings.TrimSpace(*p.Path) == "" {
			return fmt.Errorf("log file path is empty")
		}
		if r := p.Rotating; r != nil {
			for _, n := range []*int{r.MaxSize, r.MaxFileAge, r.MaxFiles} {
				if n != nil && *n < 0 {
					return fmt.Errorf("log file rotating values must not be negative")
				}
			}
		}
	}
	seen := make(map[string]struct{}, len(config.Modules))
	for _, rule := range config.Modules {
		if err := validateModule(rule.Module); err != nil {
			return err
		}
		if strings.ContainsAny(strings.TrimSuffix(rule.Module, "*"), "*?[]") {
			return fmt.Errorf("log module policy %q must be exact or end with a single *", rule.Module)
		}
		if _, exists := seen[rule.Module]; exists {
			return fmt.Errorf("log module policy %q is duplicated", rule.Module)
		}
		seen[rule.Module] = struct{}{}
		if err := validatePolicy(rule.Level, rule.FilterKeys); err != nil {
			return fmt.Errorf("log module policy %q: %w", rule.Module, err)
		}
	}
	return nil
}

func validatePolicy(level *string, keys []string) error {
	if level != nil {
		if _, valid := parseLevel(*level); !valid {
			return fmt.Errorf("log level must be debug, info, warn, error or fatal")
		}
	}
	return validateFilterKeys(keys)
}

// ApplyRuntimeConfig 原子发布所有活动实例的输出策略；准备失败保留旧输出。
// 输入由入口深拷贝；输出所有权仍属于 NewLogger 返回的 cleanup。
func ApplyRuntimeConfig(config *RuntimeConfig) error { return processState.applyRuntimeConfig(config) }

func cloneValue[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cpy := *value
	return &cpy
}

func cloneRuntimeConfig(c *RuntimeConfig) *RuntimeConfig {
	next := &RuntimeConfig{Level: cloneValue(c.Level), FilterKeys: slices.Clone(c.FilterKeys), Modules: make([]ModulePolicy, len(c.Modules))}
	for i, rule := range c.Modules {
		next.Modules[i] = ModulePolicy{Module: rule.Module, Level: cloneValue(rule.Level), Disable: cloneValue(rule.Disable), FilterKeys: slices.Clone(rule.FilterKeys)}
	}
	if p := c.Std; p != nil {
		next.Std = &OutputPolicy{Level: cloneValue(p.Level), Disable: cloneValue(p.Disable), FilterKeys: slices.Clone(p.FilterKeys)}
	}
	if p := c.File; p != nil {
		next.File = &FilePolicy{Enable: cloneValue(p.Enable), Level: cloneValue(p.Level), Path: cloneValue(p.Path), FilterKeys: slices.Clone(p.FilterKeys)}
		if r := p.Rotating; r != nil {
			next.File.Rotating = &RotatingPolicy{Disable: cloneValue(r.Disable), MaxSize: cloneValue(r.MaxSize), MaxFileAge: cloneValue(r.MaxFileAge), MaxFiles: cloneValue(r.MaxFiles), LocalTime: cloneValue(r.LocalTime), Compress: cloneValue(r.Compress)}
		}
	}
	return next
}

func applyValue[T any](target *T, value *T) {
	if value != nil {
		*target = *value
	}
}

func mergeOutputConfig(base envConfig, policy *RuntimeConfig) (envConfig, error) {
	if policy != nil {
		// 根策略先覆盖环境输出级别，再由输出端显式配置覆盖。
		if policy.Level != nil {
			level, _ := parseLevel(*policy.Level)
			base.Level, base.Std.Level, base.File.Level = level, level, level
		}
		if p := policy.Std; p != nil {
			applyValue(&base.Std.Disable, p.Disable)
			if p.Level != nil {
				base.Std.Level, _ = parseLevel(*p.Level)
			}
			if p.FilterKeys != nil {
				base.Std.FilterKeys = p.FilterKeys
			}
		}
		if p := policy.File; p != nil {
			if p.Enable != nil {
				base.File.Disable = !*p.Enable
			}
			applyValue(&base.File.Path, p.Path)
			if p.Level != nil {
				base.File.Level, _ = parseLevel(*p.Level)
			}
			if p.FilterKeys != nil {
				base.File.FilterKeys = p.FilterKeys
			}
			if r := p.Rotating; r != nil {
				applyValue(&base.File.Rotating.Disable, r.Disable)
				applyValue(&base.File.Rotating.MaxSize, r.MaxSize)
				applyValue(&base.File.Rotating.MaxFileAge, r.MaxFileAge)
				applyValue(&base.File.Rotating.MaxFiles, r.MaxFiles)
				applyValue(&base.File.Rotating.LocalTime, r.LocalTime)
				applyValue(&base.File.Rotating.Compress, r.Compress)
			}
		}
	}
	return base, validateConfig(base)
}
