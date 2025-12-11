package config

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/job"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"google.golang.org/protobuf/proto"
)

type JobConfig = kratos_foundation_pb.JobComponent_JobConfig_Job
type ConcurrentPolicy = kratos_foundation_pb.JobComponent_JobConfig_Job_ConcurrentPolicy

type ScheduleConfiguration interface {
	Schedule() string
}

type ImmediatelyConfiguration interface {
	Immediately() bool
}

type ConcurrentPolicyConfiguration interface {
	ConcurrentPolicy() ConcurrentPolicy
}

func GetJobConfig(
	config *Config,
	name string,
	job job.Job,
) (jobConf *JobConfig) {
	jobConf = &JobConfig{}

	// 默认值为 job 实例上的配置
	if t, ok := job.(ScheduleConfiguration); ok {
		jobConf.Schedule = t.Schedule()
	}
	if t, ok := job.(ImmediatelyConfiguration); ok {
		jobConf.Immediately = t.Immediately()
	}
	if t, ok := job.(ConcurrentPolicyConfiguration); ok {
		jobConf.ConcurrentPolicy = t.ConcurrentPolicy()
	}

	// 如果存在配置文件的配置，则覆盖
	if config.GetJobs() != nil {
		if conf, ok := config.GetJobs()[name]; ok {
			proto.Merge(jobConf, conf)
		}
	}

	return
}
