package bootstrap

import (
	"fmt"
	"slices"
	"strings"
	"text/template"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// LocalConfigPaths 是业务声明的有序本地路径模板；独立类型供 Wire 区分依赖。
type LocalConfigPaths []string

// RemoteConfigPaths 是业务声明的有序远程路径模板；独立类型供 Wire 区分依赖。
type RemoteConfigPaths []string

// ConfigSources 描述应用默认配置来源，供 Wire 按具体类型注入。
// 零值不登记默认来源；启用时须提供 AppInfo 和两个来源构造函数。
// NewSpec 复制选中的路径列表，AppInfo 及函数仍共享；构造阶段不执行配置 I/O。
type ConfigSources struct {
	// AppInfo 提供模板变量 app 和 version。
	AppInfo appinfo.AppInfo
	// LocalPaths 指定 local 环境使用的路径模板；空列表禁用该来源。
	LocalPaths LocalConfigPaths
	// RemotePaths 指定其他环境使用的路径模板；空列表禁用该来源。
	RemotePaths RemoteConfigPaths
	// LocalSource 按模板替换后的路径创建延迟加载器，资源由配置 Manager 释放。
	LocalSource func(...string) config.SourceLoader
	// RemoteSource 按模板替换后的路径创建延迟加载器，资源由配置 Manager 释放。
	RemoteSource func(...string) config.SourceLoader
}

// loader 在 NewSpec 中固定环境和路径列表，模板解析和来源 I/O 延迟到 NewConfigManager。
func (sources ConfigSources) loader() config.SourceLoader {
	if sources.LocalSource == nil && sources.RemoteSource == nil && sources.AppInfo == nil && sources.LocalPaths == nil && sources.RemotePaths == nil {
		return nil
	}
	if sources.AppInfo == nil || sources.LocalSource == nil || sources.RemoteSource == nil {
		panic("bootstrap: incomplete config sources")
	}
	environment := env.AppEnv()
	patterns, source, kind := []string(sources.RemotePaths), sources.RemoteSource, "remote"
	if environment == env.Local {
		patterns, source, kind = sources.LocalPaths, sources.LocalSource, "local"
	}
	// 固定选中列表，调用方后续修改切片不影响已登记的加载器。
	patterns = slices.Clone(patterns)
	return func() (config.Sources, error) {
		paths, err := renderConfigPaths(patterns, template.FuncMap{
			// 使用闭包返回固定环境，避免模板执行时重新读取进程环境。
			"env":     func() string { return environment },
			"app":     sources.AppInfo.Name,
			"version": sources.AppInfo.Version,
		})
		if err != nil {
			return nil, fmt.Errorf("resolve %s config paths: %w", kind, err)
		}
		// 完整列表先编译成功，再交给来源解析目录、精确路径和 glob，避免部分模板失败后执行 I/O。
		var loaded config.Sources
		if len(paths) > 0 {
			loaded, err = source(paths...)()
			if err != nil {
				return nil, err
			}
		}
		if len(loaded) == 0 {
			log.WithModule("bootstrap").With("function", "ConfigSources.loader", "event", "config.sources.empty", "env", environment, "name", sources.AppInfo.Name()).Warn("No configuration sources available; continuing with env and any additional sources")
		}
		return loaded, nil
	}
}

// renderConfigPaths 使用标准 Go 文本模板；引用未知函数或空白结果必须报错，不能扩大路径匹配范围。
func renderConfigPaths(patterns []string, functions template.FuncMap) ([]string, error) {
	paths := make([]string, len(patterns))
	for index, pattern := range patterns {
		tmpl, err := template.New("config path").Funcs(functions).Option("missingkey=error").Parse(pattern)
		if err != nil {
			return nil, fmt.Errorf("parse config path template %d: %w", index, err)
		}
		var result strings.Builder
		if err := tmpl.Execute(&result, nil); err != nil {
			return nil, fmt.Errorf("execute config path template %d: %w", index, err)
		}
		if strings.TrimSpace(result.String()) == "" {
			return nil, fmt.Errorf("config path template %d rendered an empty path", index)
		}
		paths[index] = result.String()
	}
	return paths, nil
}
