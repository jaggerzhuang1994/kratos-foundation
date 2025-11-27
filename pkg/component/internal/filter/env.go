package filter

import (
	"github.com/go-kratos/kratos/v2/selector"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
)

// Env 指定 optionalEnv 环境，或者自动根据当前 env 环境自动选择
func Env(optionalEnv ...string) selector.NodeFilter {
	// 指定环境
	if len(optionalEnv) > 0 {
		return Switch(
			utils.Map(optionalEnv, func(env string) selector.NodeFilter {
				return Metadata(map[string]string{
					app_info.MdEnv: env,
				})
			})...,
		)
	}

	// > 如果当前环境是 local，则优先过滤当前本机服务（根据hostname）
	if env.IsLocal() {
		return Switch(
			// 查找相同主机下的local服务
			Metadata(map[string]string{
				app_info.MdEnv:      env.AppEnv(),
				app_info.MdHostname: app_info.Hostname,
			}),
			// 查找其他机器上的local服务，可能存在与夸机器调用其他人的服务的场景
			Metadata(map[string]string{
				app_info.MdEnv: env.AppEnv(),
			}),
		)
	}

	// > 如果非local，则过滤 env = 当前环境
	return Metadata(map[string]string{
		app_info.MdEnv: env.AppEnv(),
	})
}
