package cron

import (
	"context"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/job"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/robfig/cron/v3"
)

type Cron struct {
	log    *log.Log
	cron   *cron.Cron
	parser cron.ScheduleParser
}

func NewCron(
	log *log.Log,
	conf *config.Config,
	parser ScheduleParser,
	cronLogger CronLogger,
) (*Cron, error) {
	opt := []cron.Option{
		cron.WithParser(parser),
		cron.WithLogger(cronLogger),
	}
	if conf.GetTimezone() != "" {
		tz, err := time.LoadLocation(conf.GetTimezone())
		if err != nil {
			return nil, err
		}
		opt = append(opt, cron.WithLocation(tz))
	}
	cc := cron.New(opt...)
	return &Cron{
		log:    log,
		cron:   cc,
		parser: parser,
	}, nil
}

func (c *Cron) Schedule(ctx context.Context, name string, job job.Job, schedule Schedule) cron.EntryID {
	return c.cron.Schedule(schedule, &cronJob{
		name: name,
		ctx:  ctx,
		job:  job,
	})
}

func (c *Cron) Start() {
	c.cron.Start()
}

func (c *Cron) Stop() {
	c.log.Info("stop cron")
	<-c.cron.Stop().Done()
	c.log.Info("stop cron done")
}
