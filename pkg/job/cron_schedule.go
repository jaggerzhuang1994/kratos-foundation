package job

import (
	"time"

	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
)

type Schedule = cron.Schedule

type jobScheduleConfig interface {
	GetName() string
	GetSchedule() string
	GetImmediately() bool
}

type ScheduleParser interface {
	cron.ScheduleParser
	ParseJob(jobScheduleConfig) (Schedule, error)
}

type schedule struct {
	log                  CronLog
	immediately          bool
	schedule             Schedule
	immediatelyScheduled bool
}

type scheduleParser struct {
	log    CronLog
	parser cron.ScheduleParser
}

func NewScheduleParser(log CronLog) ScheduleParser {
	return &scheduleParser{
		log: log,
		parser: cron.NewParser(
			cron.SecondOptional |
				cron.Minute |
				cron.Hour |
				cron.Dom |
				cron.Month |
				cron.Dow |
				cron.Descriptor,
		),
	}
}

func (p *scheduleParser) Parse(spec string) (Schedule, error) {
	return p.parser.Parse(spec)
}

func (p *scheduleParser) ParseJob(jobConfig jobScheduleConfig) (Schedule, error) {
	s, err := p.parser.Parse(jobConfig.GetSchedule())
	if err != nil {
		return nil, errors.WithMessage(err, "parse schedule error")
	}
	return &schedule{
		log:         p.log.With("job", jobConfig.GetName()),
		immediately: jobConfig.GetImmediately(),
		schedule:    s,
	}, nil
}

func (s *schedule) Next(now time.Time) time.Time {
	// 如果是立刻调度，并且是首次调度
	if s.immediately && !s.immediatelyScheduled {
		s.immediatelyScheduled = true
		s.log.Debug("scheduling job immediately")
		return now
	}

	next := s.schedule.Next(now)
	s.log.With("next", next.Format(time.RFC3339), "left", time.Until(next)).Debug("job scheduled")
	return next
}
