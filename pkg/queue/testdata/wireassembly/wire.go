//go:build wireinject

package wireassembly

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

func initialize(stores queueStores, info appinfo.AppInfo, spec *bootstrap.Spec) (*typedQueues, func(), error) {
	wire.Build(bootstrap.BaseProviderSet, newEmailQueue, newBotQueue, newEmailService, newEmailWorker, newBotWorker, wire.Struct(new(typedQueues), "*"))
	return nil, nil, nil
}

func initializePublisher(stores queueStores, info appinfo.AppInfo, spec *bootstrap.Spec) (*queue.Queue[EmailMessage], func(), error) {
	wire.Build(bootstrap.BaseProviderSet, newEmailQueue)
	return nil, nil, nil
}
