package wireassembly

import (
	"context"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// 队列身份只存在于组装层；相同 string 消息不会导致 Wire 实例冲突。
type emailQueue struct{ *queue.Endpoint[string] }
type botQueue struct{ *queue.Endpoint[string] }
type queueStores struct{ email, bot queue.Store }
type typedQueues struct {
	Email emailSender
	Bot   botSender
}

func newEmailQueue(stores queueStores, obs queue.Observability) (*emailQueue, error) {
	endpoint, err := queue.NewEndpoint(queue.Definition[string]{Queue: "mail", MessageType: "send", Version: 1}, stores.email, queue.Handle(deliverText), queue.ConsumerConfig{}, obs)
	if err != nil {
		return nil, err
	}
	return &emailQueue{Endpoint: endpoint}, nil
}

func newBotQueue(stores queueStores, obs queue.Observability) (*botQueue, error) {
	endpoint, err := queue.NewEndpoint(queue.Definition[string]{Queue: "bot", MessageType: "send", Version: 1}, stores.bot, queue.Handle(deliverText), queue.ConsumerConfig{}, obs)
	if err != nil {
		return nil, err
	}
	return &botQueue{Endpoint: endpoint}, nil
}

// 业务只声明自己需要的发送能力，不依赖 queue、Wire 或 Runtime。
type emailSender interface {
	Publish(context.Context, string) (string, error)
}
type botSender interface {
	Publish(context.Context, string) (string, error)
}

func deliverText(context.Context, string) error { return nil }
