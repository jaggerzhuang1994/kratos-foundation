package job

import (
	"context"

	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/context"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware/concurrent_policy"
)

type Server transport.Server

type server struct {
	cron Cron

	serverJobs []*jobConfig
	cronJobs   []struct {
		*jobConfig
		Schedule
	}

	cancel context.CancelFunc
}

func NewServer(
	log Log,
	middlewares Middlewares,
	register Register,
	cron Cron,
	parser ScheduleParser,
) (Server, error) {
	var serverJobs []*jobConfig
	var cronJobs []struct {
		*jobConfig
		Schedule
	}

	for _, jc := range register.getRegisterJobs() {
		if jc.GetDisable() {
			continue
		}
		if jc.GetSchedule() == "" {
			serverJobs = append(serverJobs, jc.middleware(middlewares))
		} else {
			s, err := parser.ParseJob(jc)
			if err != nil {
				return nil, err
			}
			cronJobs = append(cronJobs, struct {
				*jobConfig
				Schedule
			}{
				jobConfig: jc.middleware(append([]middleware.Middleware{
					concurrent_policy.Middleware(log, jc.GetConcurrentPolicy()),
				}, middlewares...)),
				Schedule: s,
			})
		}
	}

	return &server{
		cron:       cron,
		serverJobs: serverJobs,
		cronJobs:   cronJobs,
	}, nil
}

func (s *server) Start(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)

	for _, cjob := range s.cronJobs {
		s.cron.Schedule(ctx, cjob.name, cjob.job, cjob.Schedule)
	}
	s.cron.start()

	// 异步执行 serverJob
	for _, sjob := range s.serverJobs {
		go func(sjob *jobConfig) {
			_ = sjob.job.Run(jobcontext.WithJobName(ctx, sjob.name))
		}(sjob)
	}

	return nil
}

func (s *server) Stop(_ context.Context) error {
	s.cancel()
	s.cron.stop()
	return nil
}
