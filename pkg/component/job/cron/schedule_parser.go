package cron

import "github.com/robfig/cron/v3"

type ScheduleParser = cron.ScheduleParser

func NewScheduleParser() ScheduleParser {
	return cron.NewParser(
		cron.SecondOptional |
			cron.Minute |
			cron.Hour |
			cron.Dom |
			cron.Month |
			cron.Dow |
			cron.Descriptor,
	)
}
