package job

import (
	"context"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

type defaultsTask struct{}

func (defaultsTask) Run(context.Context) error          { return nil }
func (defaultsTask) Schedule() string                   { return "@hourly" }
func (defaultsTask) ConcurrentPolicy() ConcurrentPolicy { return DelayIfRunning }
func (defaultsTask) RunImmediately() bool               { return true }
func (defaultsTask) MaxPendingRuns() int                { return 8 }

func TestCronConfigurationPriority(t *testing.T) {
	for _, tc := range []struct {
		name     string
		task     Task
		schedule string
		options  []CronOption
		override *config_pb.CronJob
		want     cronConfig
	}{
		{name: "framework defaults", task: TaskFunc(func(context.Context) error { return nil }), schedule: "@daily", want: cronConfig{schedule: "@daily", policy: AllowOverlap, pending: 1}},
		{name: "task defaults", task: defaultsTask{}, want: cronConfig{schedule: "@hourly", policy: DelayIfRunning, immediate: true, pending: 8}},
		{name: "registration including zero", task: defaultsTask{}, schedule: "@daily", options: []CronOption{RunImmediately(false), WithConcurrentPolicy(AllowOverlap), WithMaxPendingRuns(0)}, want: cronConfig{schedule: "@daily", policy: AllowOverlap, pending: 0}},
		{name: "configuration including zero", task: defaultsTask{}, schedule: "@daily", options: []CronOption{RunImmediately(true), WithConcurrentPolicy(SkipIfRunning), WithMaxPendingRuns(3)}, override: &config_pb.CronJob{Schedule: proto.String("@weekly"), RunImmediately: proto.Bool(false), ConcurrentPolicy: config_pb.JobConcurrentPolicy_ALLOW_OVERLAP.Enum(), MaxPendingRuns: proto.Int32(0)}, want: cronConfig{schedule: "@weekly", policy: AllowOverlap, pending: 0}},
		{name: "per field fallback", task: defaultsTask{}, schedule: "@daily", options: []CronOption{WithMaxPendingRuns(3)}, override: &config_pb.CronJob{RunImmediately: proto.Bool(false)}, want: cronConfig{schedule: "@daily", policy: DelayIfRunning, pending: 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := NewSpec()
			spec.RegisterCron("refresh", tc.schedule, tc.task, tc.options...)
			base := taskCronConfig(spec.definitions[0])
			got, err := resolveCronConfig(base, tc.override)
			if err != nil || got != tc.want {
				t.Fatalf("got=%+v err=%v want=%+v", got, err, tc.want)
			}
			restored, err := resolveCronConfig(base, nil)
			if err != nil || restored != base {
				t.Fatalf("removed override did not restore baseline: %+v %v", restored, err)
			}
		})
	}
}

func TestManagerValidatesMergedCronConfiguration(t *testing.T) {
	logger, tracing, metrics := newTestObservability(t)
	for _, tc := range []struct {
		name     string
		cfg      *config_pb.Job
		schedule string
		options  []CronOption
		valid    bool
	}{
		{name: "config supplies missing expression", cfg: &config_pb.Job{Cron: map[string]*config_pb.CronJob{"task": {Schedule: proto.String("@hourly")}}}, valid: true},
		{name: "invalid cron", schedule: "broken"},
		{name: "missing expression"},
		{name: "invalid registration policy", schedule: "@hourly", options: []CronOption{WithConcurrentPolicy(255)}},
		{name: "invalid registration capacity", schedule: "@hourly", options: []CronOption{WithMaxPendingRuns(-2)}},
		{name: "overflow capacity", schedule: "@hourly", options: []CronOption{WithMaxPendingRuns(int(^uint(0) >> 1))}},
		{name: "unknown policy", schedule: "@hourly", cfg: &config_pb.Job{Cron: map[string]*config_pb.CronJob{"task": {ConcurrentPolicy: config_pb.JobConcurrentPolicy(256).Enum()}}}},
		{name: "unknown task", schedule: "@hourly", cfg: &config_pb.Job{Cron: map[string]*config_pb.CronJob{"typo": {}}}},
		{name: "invalid config capacity", schedule: "@hourly", cfg: &config_pb.Job{Cron: map[string]*config_pb.CronJob{"task": {MaxPendingRuns: proto.Int32(-2)}}}},
		{name: "valid config overrides invalid registration", schedule: "invalid", options: []CronOption{WithMaxPendingRuns(-2)}, cfg: &config_pb.Job{Cron: map[string]*config_pb.CronJob{"task": {Schedule: proto.String("@daily"), MaxPendingRuns: proto.Int32(1)}}}, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := NewSpec()
			spec.RegisterCron("task", tc.schedule, TaskFunc(func(context.Context) error { return nil }), tc.options...)
			cfg := tc.cfg
			if cfg == nil {
				cfg = &config_pb.Job{}
			}
			_, err := NewManager(logger, spec, tracing, metrics, testconfig.New(t, "job", cfg))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
