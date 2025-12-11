package job

import (
	"context"

	log2 "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/cron"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/job"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/middleware/concurrent_policy"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/middleware/logging"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/middleware/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/middleware/recovery"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/otel"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/pkg/errors"
)

// Server 将 job 包装成 kratos 的 transport.Server 注册到 app.Server 中运行
type Server struct {
	log  *log.Log
	cron *cron.Cron

	serverJobs []serverJob
	cronJobs   []cronJob

	cancel context.CancelFunc
}

// 从 registerJob -> serverJob
type serverJob struct {
	name string
	job  job.Job
}

// 从 registerJob -> cronJob
type cronJob struct {
	serverJob
	schedule cron.Schedule
}

func NewServer(
	conf *config.Config,
	log *log.Log,
	tp *otel.TracingProvider,
	mp *otel.MetricsProvider,
	cron_ *cron.Cron,
	parser cron.ScheduleParser,
	register *Register,
) (*Server, error) {
	if conf.GetDisable() {
		return nil, nil
	}

	log = log.WithModule("job", conf.GetLog()).
		With(
			"job", log2.Valuer(func(ctx context.Context) any {
				return job.GetName(ctx)
			}),
		)

	var serverJobs []serverJob
	var cronJobs []cronJob

	recoveryMiddleware := recovery.Middleware(log)
	restMiddleware := []middleware.Middleware{
		tracing.Middleware(tp),
		metrics.Middleware(mp),
		logging.Middleware(log),
	}
	serverJobMiddleware := append([]middleware.Middleware{
		recoveryMiddleware,
	}, restMiddleware...)

	for _, registerJob := range register.Jobs {
		jobLog := log.With("job", registerJob.Name)

		jobConf := config.GetJobConfig(conf, registerJob.Name, registerJob.Job)
		if jobConf.GetDisable() {
			continue
		}
		if jobConf.GetSchedule() == "" {
			serverJobs = append(serverJobs, serverJob{
				name: registerJob.Name,
				job:  job.FuncJob(middleware.Chain(serverJobMiddleware...)(registerJob.Job.Run)),
			})
		} else {
			jobLog.Infof("jobConf schedule=%s concurrent_policy=%s immediately=%v", jobConf.GetSchedule(), jobConf.GetConcurrentPolicy(), jobConf.GetImmediately())
			schedule, err := parser.Parse(jobConf.GetSchedule())
			if err != nil {
				return nil, errors.WithMessagef(err, "parse job %s schedule %s error", registerJob.Name, jobConf.GetSchedule())
			}
			schedule = cron.NewSchedule(jobLog, jobConf.GetImmediately(), schedule)

			chain := []middleware.Middleware{
				recoveryMiddleware, concurrent_policy.Middleware(jobLog, jobConf.GetConcurrentPolicy()),
			}
			chain = append(chain, restMiddleware...)

			cronJobs = append(cronJobs, cronJob{
				serverJob: serverJob{
					name: registerJob.Name,
					job:  job.FuncJob(middleware.Chain(chain...)(registerJob.Job.Run)),
				},
				schedule: schedule,
			})
		}
	}

	return &Server{
		log:        log,
		cron:       cron_,
		serverJobs: serverJobs,
		cronJobs:   cronJobs,
	}, nil
}

func (s *Server) Start(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)

	for _, cjob := range s.cronJobs {
		s.cron.Schedule(ctx, cjob.name, cjob.job, cjob.schedule)
	}

	s.cron.Start()

	// 异步执行serverJob
	for _, sjob := range s.serverJobs {
		go func(sjob serverJob) {
			_ = job.Run(ctx, sjob.name, sjob.job)
		}(sjob)
	}

	return nil
}

func (s *Server) Stop(_ context.Context) error {
	s.cancel()
	s.cron.Stop()
	return nil
}
