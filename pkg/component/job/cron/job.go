package cron

import (
	"context"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/job"
)

type cronJob struct {
	name string
	ctx  context.Context
	job  job.Job
}

func (j *cronJob) Run() {
	_ = job.Run(j.ctx, j.name, j.job)
}
