package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
)

// JobBootstrap 标记 Job Manager 已同步登记到应用 Spec。
type JobBootstrap struct{}

// NewJobBootstrap 在 Manager 有任务时将其登记为应用 Runtime。
func NewJobBootstrap(spec *app.Spec, manager *job.Manager) (JobBootstrap, error) {
	if !manager.HasJobs() {
		return JobBootstrap{}, nil
	}
	if err := spec.RegisterRuntime(&jobRuntime{Manager: manager}); err != nil {
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
