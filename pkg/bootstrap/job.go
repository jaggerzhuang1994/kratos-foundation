package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

// JobBootstrap 标记 Job Manager 已同步登记到应用 Spec。
type JobBootstrap struct{}

// NewJobBootstrap 在业务 Boot 完成后构造并登记选中的任务，不启动任务。
// 未选择 Job 时不创建 Manager；Spec 仅允许组装一次，失败后应丢弃。
func NewJobBootstrap(
	spec *Spec,
	coordinator job.ConcurrencyCoordinator,
	logger log.Logger,
	meter metrics.Provider,
	tracer tracing.Provider,
	_ Bootstrap,
) (JobBootstrap, error) {
	if spec == nil || spec.assembled || spec.jobsBuilt {
		return JobBootstrap{}, fmt.Errorf("job bootstrap: spec is nil or already assembled")
	}
	spec.jobsBuilt = true
	if spec.jobs == nil {
		return JobBootstrap{}, nil
	}
	manager, err := job.NewManager(logger, spec.jobs, tracer, meter, coordinator)
	if err != nil {
		return JobBootstrap{}, fmt.Errorf("job bootstrap: %w", err)
	}
	if !manager.HasJobs() {
		return JobBootstrap{}, nil
	}
	if err := ApplicationSpec(spec).RegisterRuntime(&jobRuntime{Manager: manager}); err != nil {
		return JobBootstrap{}, fmt.Errorf("job bootstrap: register job runtime: %w", err)
	}
	return JobBootstrap{}, nil
}

// jobRuntime 在组装边界将任务完成转换为应用正常停止，领域包无需了解 App。
type jobRuntime struct{ *job.Manager }

func (r *jobRuntime) Start(ctx context.Context) error {
	err := r.Manager.Start(ctx)
	// Manager 的完成信号为独立哨兵；包含该哨兵的任务错误仍应原样传播。
	if errors.Is(err, job.ErrCompleted) {
		return app.ErrStopRequested
	}
	return err
}
