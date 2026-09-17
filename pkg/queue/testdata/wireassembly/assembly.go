package wireassembly

import (
	"context"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

type EmailMessage string
type BotMessage string
type queueStores struct{ email, bot queue.Store }
type typedQueues struct {
	Email         *queue.Queue[EmailMessage]
	Bot           *queue.Queue[BotMessage]
	EmailWorker   *queue.Worker[EmailMessage]
	BotWorker     *queue.Worker[BotMessage]
	Observability queue.Observability
	Logger        log.Logger
	Metrics       metrics.Provider
	Tracing       tracing.Provider
}

func newEmailQueue(stores queueStores, observability queue.Observability) (*queue.Queue[EmailMessage], error) {
	return queue.NewQueue(queue.Definition[EmailMessage]{Queue: "mail", Version: 1}, stores.email, observability)
}
func newBotQueue(stores queueStores, observability queue.Observability) (*queue.Queue[BotMessage], error) {
	return queue.NewQueue(queue.Definition[BotMessage]{Queue: "bot", Version: 1}, stores.bot, observability)
}

// 服务依赖投递入口，Worker 再依赖服务，构造图不形成循环。
type emailService struct{ queue *queue.Queue[EmailMessage] }

func newEmailService(q *queue.Queue[EmailMessage]) *emailService   { return &emailService{queue: q} }
func (s *emailService) Handle(context.Context, EmailMessage) error { return nil }
func newEmailWorker(q *queue.Queue[EmailMessage], service *emailService) (*queue.Worker[EmailMessage], error) {
	return q.Worker(service.Handle, queue.WorkerConfig{DisableProcessing: true})
}
func newBotWorker(q *queue.Queue[BotMessage]) (*queue.Worker[BotMessage], error) {
	return q.Worker(func(context.Context, BotMessage) error { return nil }, queue.WorkerConfig{DisableProcessing: true})
}
