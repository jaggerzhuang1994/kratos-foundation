package snapshot

import (
	"errors"
	"os"

	"github.com/compose-spec/compose-go/v2/template"
)

// expandEnvironment 在格式解析前执行 Compose 环境模板替换，不解析配置自身引用。
func expandEnvironment(value string) (string, error) {
	expanded, err := template.SubstituteWithOptions(value, os.LookupEnv, template.WithoutLogging)
	if err != nil {
		// 上游语法错误携带完整模板；不能让配置正文进入 Manager 的错误日志。
		var invalid *template.InvalidTemplateError
		if errors.As(err, &invalid) {
			return "", errors.New("invalid environment template")
		}
		return "", err
	}
	return expanded, nil
}
