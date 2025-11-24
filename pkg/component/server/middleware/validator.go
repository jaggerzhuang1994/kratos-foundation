package middleware

import (
	"context"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

func Validator() middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if validator, ok := req.(interface{ ValidateAll() error }); ok {
				if err := validator.ValidateAll(); err != nil {
					var errs []error
					getAllErrors, ok := err.(interface{ AllErrors() []error })
					if ok {
						errs = getAllErrors.AllErrors()
					} else {
						errs = []error{err}
					}
					md := map[string]string{}
					for _, e := range errs {
						if validErr, ok := e.(interface {
							Field() string
							Reason() string
							Cause() error
						}); ok {
							md[validErr.Field()] = validErr.Reason()
						}
					}
					return nil, kratos_foundation_pb.ErrorValidator("request invalid").WithCause(err).WithMetadata(md)
				}
			}
			return handler(ctx, req)
		}
	}
}
