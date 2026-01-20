package job

type Register interface {
	Register(name string, job Job)
	getRegisterJobs() []*jobConfig
}

type register struct {
	config       Config
	registerJobs []*jobConfig
}

func NewRegister(config Config) Register {
	return &register{
		config: config,
	}
}

func (r *register) Register(name string, job Job) {
	r.registerJobs = append(r.registerJobs, getJobConfig(r.config, name, job))
}

func (r *register) getRegisterJobs() []*jobConfig {
	return r.registerJobs
}
