package app

import (
	"context"

	"github.com/go-kratos/kratos/v2"
)

type OnInitContextHook interface {
	OnInitContext(context.Context) context.Context
}

type OnInitOptionHook interface {
	OnInitOption([]kratos.Option) []kratos.Option
}

type OnBeforeStartHook interface {
	OnBeforeStart(context.Context) error
}

type OnAfterStartHook interface {
	OnAfterStart(context.Context) error
}

type OnBeforeStopHook interface {
	OnBeforeStop(context.Context) error
}

type OnAfterStopHook interface {
	OnAfterStop(context.Context) error
}
