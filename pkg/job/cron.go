package job

import (
	"context"
	"time"

	jobcontext "github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/context"
	"github.com/robfig/cron/v3"
)

type Cron interface {
	Schedule(context.Context, string, Job, Schedule) cron.EntryID
	Remove(cron.EntryID)
	start()
	stop()
}

type cron_ struct {
	log    Log
	cron   *cron.Cron
	parser ScheduleParser
}

func NewCron(
	log CronLog,
	config Config,
	parser ScheduleParser,
	logger CronLogger,
) (Cron, error) {
	opt := []cron.Option{
		cron.WithParser(parser),
		cron.WithLogger(logger),
	}
	if config.GetTimezone() != "" {
		tz, err := time.LoadLocation(config.GetTimezone())
		if err != nil {
			return nil, err
		}
		opt = append(opt, cron.WithLocation(tz))
	}
	c := &cron_{
		log:    log,
		cron:   cron.New(opt...),
		parser: parser,
	}
	return c, nil
}

func (c *cron_) Schedule(ctx context.Context, name string, job Job, schedule Schedule) cron.EntryID {
	return c.cron.Schedule(schedule, &cronJob{
		name: name,
		ctx:  jobcontext.WithJobName(ctx, name),
		job:  job,
	})
}

func (c *cron_) Remove(id cron.EntryID) {
	c.cron.Remove(id)
}

func (c *cron_) start() {
	c.log.Info("start cron")
	c.cron.Start()
}

func (c *cron_) stop() {
	c.log.Info("stop cron")
	<-c.cron.Stop().Done()
	c.log.Info("stop cron done")
}

type cronJob struct {
	name string
	ctx  context.Context
	job  Job
}

func (j *cronJob) Run() {
	_ = j.job.Run(j.ctx)
}
