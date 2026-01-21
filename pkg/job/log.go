package job

import (
	"context"

	log2 "github.com/go-kratos/kratos/v2/log"
	jobcontext "github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/context"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
)

type Log log.Log

var jobNameValuer = log2.Valuer(func(ctx context.Context) any {
	return jobcontext.GetJobName(ctx)
})

func NewLog(log log.Log, config Config) Log {
	return log.WithModule("job", config.GetLog()).With("job", jobNameValuer)
}

type CronLog log.Log

func NewCronLog(log log.Log, config Config) CronLog {
	return log.WithModule("job/cron", config.GetLog()).With("job", jobNameValuer)
}
