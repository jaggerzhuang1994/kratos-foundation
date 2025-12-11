package cron

import (
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/robfig/cron/v3"
)

type Schedule = cron.Schedule

type scheduleWrapper struct {
	log         *log.Log
	immediately bool
	schedule    Schedule

	immediatelyScheduled bool
}

func NewSchedule(
	log *log.Log,
	immediately bool,
	schedule Schedule,
) Schedule {
	return &scheduleWrapper{
		log:         log,
		immediately: immediately,
		schedule:    schedule,
	}
}

func (s *scheduleWrapper) Next(now time.Time) time.Time {
	// 如果是立刻调度，并且是首次调度
	if s.immediately && !s.immediatelyScheduled {
		s.immediatelyScheduled = true
		s.log.Debug("job schedule immediately")
		return now
	}

	next := s.schedule.Next(now)
	s.log.With("next", next.Format(time.RFC3339), "left", next.Sub(time.Now())).Debug("job schedule")
	return next
}
