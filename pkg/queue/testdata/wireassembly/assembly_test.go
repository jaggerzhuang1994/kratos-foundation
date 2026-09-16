package wireassembly

import (
	"context"
	"testing"

	textconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

type captureStore struct {
	queue.Store
	task *queue.Task
}

func (s *captureStore) Enqueue(_ context.Context, task *queue.Task) error { s.task = task; return nil }

func TestBindings(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("LOG_STD_DISABLE", "true")
	t.Setenv("LOG_FILE_ENABLE", "false")
	spec := testSpec()
	mail, bot := &captureStore{}, &captureStore{}
	queues, cleanup, err := initialize(queueStores{email: mail, bot: bot}, appinfo.New("queue-wire"), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if queues.Observability.Logger != queues.Logger || queues.Observability.Tracing != queues.Tracing || queues.Observability.Metrics != queues.Metrics {
		t.Fatal("observability did not reuse application providers")
	}
	if _, err := queues.Email.Post(context.Background(), "mail"); err != nil {
		t.Fatal(err)
	}
	if _, err := queues.Bot.Post(context.Background(), "bot"); err != nil {
		t.Fatal(err)
	}
	if mail.task == nil || bot.task == nil || string(mail.task.Payload) != `"mail"` || string(bot.task.Payload) != `"bot"` {
		t.Fatal("queue identities mixed")
	}
	var _ app.Runtime = (*queue.Worker[EmailMessage])(nil)
	var _ app.Runtime = (*queue.Worker[BotMessage])(nil)
}

func TestQueueIsNotRuntime(t *testing.T) {
	if _, ok := any((*queue.Queue[EmailMessage])(nil)).(app.Runtime); ok {
		t.Fatal("queue unexpectedly owns a runtime")
	}
}

func testSpec() *bootstrap.Spec {
	return bootstrap.NewSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), bootstrap.ConfigSources{}).Configuration(func() (config.Sources, error) {
		source, err := textconfig.NewSource("queue.json", config.JSONFormat, `{"tracing":{"disable":true}}`)
		if err != nil {
			return nil, err
		}
		return config.NewSources(source), nil
	})
}

func TestPublisherOnly(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("LOG_STD_DISABLE", "true")
	t.Setenv("LOG_FILE_ENABLE", "false")
	store := &captureStore{}
	q, cleanup, err := initializePublisher(queueStores{email: store}, appinfo.New("queue-publisher"), testSpec())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := q.Post(context.Background(), EmailMessage("only publish")); err != nil {
		t.Fatal(err)
	}
	if store.task == nil {
		t.Fatal("publisher failed to enqueue")
	}
}
