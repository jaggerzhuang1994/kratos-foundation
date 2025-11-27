package validator

import (
	"context"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

type Config = kratos_foundation_pb.MiddlewareConfig_Validator

func Enable(config *Config) bool {
	return !config.GetDisable()
}

func Validator() middleware.Middleware {
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
