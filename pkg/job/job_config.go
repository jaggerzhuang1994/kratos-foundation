package job

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

type ConcurrentPolicy = config_pb.ConcurrentPolicy

type ScheduleConfiguration interface {
	Schedule() string
}

type ImmediatelyConfiguration interface {
	Immediately() bool
}

type ConcurrentPolicyConfiguration interface {
	ConcurrentPolicy() ConcurrentPolicy
}

type jobConfig struct {
	*config_pb.JobConfig
	name string
	job  Job
}

func getJobConfig(config Config, name string, job Job) (jc *jobConfig) {
	jc = &jobConfig{
		JobConfig: &config_pb.JobConfig{},
		name:      name,
		job:       job,
	}

	// 默认值为 job 实例上的配置
	if t, ok := job.(ScheduleConfiguration); ok {
		jc.Schedule = t.Schedule()
	}
	if t, ok := job.(ImmediatelyConfiguration); ok {
		jc.Immediately = proto.Bool(t.Immediately())
	}
	if t, ok := job.(ConcurrentPolicyConfiguration); ok {
		var concurrentPolicy = t.ConcurrentPolicy()
		jc.ConcurrentPolicy = &concurrentPolicy
	}

	// 如果存在配置文件的配置，则合并
	if config.GetJobs() != nil {
		if conf, ok := config.GetJobs()[name]; ok {
			proto.Merge(jc, conf)
		}
	}

	return
}

func (jc *jobConfig) GetName() string {
	return jc.name
}

func (jc *jobConfig) middleware(middlewares []middleware.Middleware) *jobConfig {
	return &jobConfig{
		JobConfig: proto.Clone(jc.JobConfig).(*config_pb.JobConfig),
		name:      jc.name,
		job:       FuncJob(middleware.Chain(middlewares...)(jc.job.Run)),
	}
}
