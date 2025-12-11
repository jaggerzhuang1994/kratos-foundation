package middleware

import (
	"context"
)

type Handler func(ctx context.Context) error

type Middleware func(Handler) Handler

// Chain returns a middleware that specifies the chained handler for endpoint.
func Chain(m ...Middleware) Middleware {
	return func(next Handler) Handler {
		for i := len(m) - 1; i >= 0; i-- {
			if m[i] != nil {
				next = m[i](next)
			}
		}
		return next
	}
}
