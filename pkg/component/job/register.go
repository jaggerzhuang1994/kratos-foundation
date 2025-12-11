package job

import "github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/job"

type RegisterJob struct {
	Name string
	Job  job.Job
}

type Register struct {
	Jobs []RegisterJob
}

func NewRegister() *Register {
	return &Register{}
}

func (r *Register) Register(name string, job job.Job) {
	r.Jobs = append(r.Jobs, RegisterJob{name, job})
}
