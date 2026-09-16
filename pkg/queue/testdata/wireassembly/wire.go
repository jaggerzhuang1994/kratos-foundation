//go:build wireinject

package wireassembly

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

func initialize(stores queueStores, obs queue.Observability) (*typedQueues, error) {
	wire.Build(newEmailQueue, newBotQueue, wire.Bind(new(emailSender), new(*emailQueue)), wire.Bind(new(botSender), new(*botQueue)), wire.Struct(new(typedQueues), "*"))
	return nil, nil
}
