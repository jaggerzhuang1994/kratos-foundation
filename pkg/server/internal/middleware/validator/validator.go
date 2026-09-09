// Package validator 提供 server 私有的请求校验中间件。
package validator

import (
	"context"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

// Config 是请求校验中间件对应的 protobuf 配置类型。
type Config = *config_pb.Middleware_Validator

// Validator 创建请求校验中间件，并把 protobuf 校验细节转换为稳定业务错误。
func Validator(config Config) middleware.Middleware {
	if config.GetDisable() {
		return nil
	}
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if validator, ok := req.(interface{ ValidateAll() error }); ok {
				if err := validator.ValidateAll(); err != nil {
					validationErr := errors.ParseValidationError(err)
					return nil, kratos_foundation_pb.ErrorValidator("request invalid").
						WithCause(err).
						WithValidationError(validationErr)
				}
			}
			return handler(ctx, req)
		}
	}
}
