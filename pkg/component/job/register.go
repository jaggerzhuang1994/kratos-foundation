package job

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/job"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
)

type RegisterJob struct {
	Name string
	Job  job.Job
}

type Register struct {
	log  *log.Log
	Jobs []RegisterJob
}

func NewRegister(
	log *log.Log,
	conf *config.Config,
) *Register {
	return &Register{
		log: log.WithModule("job/register", conf.GetLog()),
	}
}

func (r *Register) Register(name string, job job.Job) {
	r.log.Infof("register job %s", name)
	r.Jobs = append(r.Jobs, RegisterJob{name, job})
}
