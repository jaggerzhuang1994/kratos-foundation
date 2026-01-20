package validator

import (
	"context"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
)

type Config = *config_pb.Middleware_Validator

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
