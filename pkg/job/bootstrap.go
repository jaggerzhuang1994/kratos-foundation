// Package job 提供定时任务和异步任务的启动引导功能
//
// 该包整合了 Cron 定时任务和 Server 异步任务，通过 Bootstrap 接口
// 统一管理任务的注册、启动和停止。
//
// 任务类型：
//   - Cron 任务：按照 Cron 表达式定时执行的任务
//   - Server 任务：应用启动时立即执行的异步任务
//
// 使用方式：
//
//	// 注册任务
//	job.Register("my-job", MyJobHandler)
//	    .WithSchedule("0 * * * *")  // 每小时执行
//	    .WithConcurrentPolicy(concurrent_policy.Allow)
//
//	// 任务会通过 Bootstrap 自动启动
package job

import (
	"context"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/context"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware/concurrent_policy"
	server2 "github.com/jaggerzhuang1994/kratos-foundation/pkg/server"
)

// Bootstrap 任务启动引导接口
//
// 该接口定义了任务服务的初始化逻辑，用于：
//   - 收集所有注册的任务
//   - 配置任务调度器
//   - 应用任务中间件
//   - 将任务注册到服务器
//
// 注意事项：
//   - 该接口由 Wire 依赖注入自动提供
//   - 业务侧通常不需要实现此接口
//   - 任务通过 job.Register() 注册即可
type Bootstrap any

// server 任务服务器，实现了 Kratos Server 接口
//
// 该结构体管理所有任务的启动和停止：
//   - serverJobs: 启动时立即执行的异步任务
//   - cronJobs: 按照 Cron 表达式定时执行的任务
type server struct {
	cron Cron // Cron 调度器，管理定时任务

	serverJobs []*jobConfig // 异步任务列表，应用启动时立即执行
	cronJobs   []struct {   // 定时任务列表
		*jobConfig
		Schedule
	}

	cancel context.CancelFunc // 用于取消所有正在执行的任务
}

// NewBootstrap 创建任务启动引导器
//
// 该函数是任务模块的核心入口，负责：
//  1. 收集所有通过 Register 注册的任务
//  2. 根据配置禁用任务模块
//  3. 为每个任务应用中间件链
//  4. 区分 Cron 任务和 Server 任务
//  5. 将任务服务器注册到 Kratos 服务器列表
//
// 参数说明：
//   - log: 日志记录器
//   - config: 任务配置（是否禁用、日志配置等）
//   - middlewares: 全局任务中间件链
//   - register: 任务注册器，包含所有已注册的任务
//   - cron: Cron 调度器实例
//   - parser: Cron 表达式解析器
//   - serverRegister: Kratos 服务器注册器
//
// 返回：
//   - Bootstrap: 启动引导接口（返回 nil 表示不提供自定义引导）
//   - error: 解析 Cron 表达式失败时返回错误
//
// 任务分类规则：
//   - 如果任务配置了 Schedule（Cron 表达式）→ 注册为 Cron 任务
//   - 如果任务没有配置 Schedule → 注册为 Server 任务（启动时立即执行）
//
// 注意事项：
//   - Cron 任务会自动添加并发策略中间件
//   - Server 任务会在独立的 goroutine 中异步执行
func NewBootstrap(
	log Log,
	config Config,
	middlewares Middlewares,
	register Register,
	cron Cron,
	parser ScheduleParser,
	serverRegister server2.Register,
) (Bootstrap, error) {
	jlog := log.WithModule("job", config.GetLog())
	if config.GetDisable() {
		jlog.Info("disabled")
		return nil, nil
	}

	var serverJobs []*jobConfig
	var cronJobs []struct {
		*jobConfig
		Schedule
	}

	for _, jc := range register.getRegisterJobs() {
		if jc.GetDisable() {
			continue
		}
		if jc.GetSchedule() == "" {
			serverJobs = append(serverJobs, jc.middleware(middlewares))
		} else {
			s, err := parser.ParseJob(jc)
			if err != nil {
				return nil, err
			}
			cronJobs = append(cronJobs, struct {
				*jobConfig
				Schedule
			}{
				jobConfig: jc.middleware(append([]middleware.Middleware{
					concurrent_policy.Middleware(log, jc.GetConcurrentPolicy()),
				}, middlewares...)),
				Schedule: s,
			})
		}
	}

	// 注册为 server
	serverRegister.RegisterServer(&server{
		cron:       cron,
		serverJobs: serverJobs,
		cronJobs:   cronJobs,
	})
	return nil, nil
}

// Start 启动任务服务器
//
// 该方法会：
//  1. 创建可取消的上下文，用于控制所有任务的生命周期
//  2. 将所有 Cron 任务注册到调度器
//  3. 启动 Cron 调度器
//  4. 在独立的 goroutine 中异步执行所有 Server 任务
//
// 参数：
//   - ctx: 父上下文，用于控制任务服务器的生命周期
//
// 注意事项：
//   - Server 任务在独立的 goroutine 中执行，不会阻塞服务器启动
//   - 所有任务都会携带任务名称信息
func (s *server) Start(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)

	// 注册并启动所有 Cron 任务
	for _, cjob := range s.cronJobs {
		s.cron.Schedule(ctx, cjob.name, cjob.job, cjob.Schedule)
	}
	s.cron.start()

	// 异步执行所有 Server 任务
	// 每个任务在独立的 goroutine 中运行，不会相互阻塞
	for _, sjob := range s.serverJobs {
		go func(sjob *jobConfig) {
			_ = sjob.job.Run(jobcontext.WithJobName(ctx, sjob.name))
		}(sjob)
	}

	return nil
}

// Stop 停止任务服务器
//
// 该方法会：
//  1. 取消所有正在执行的任务上下文
//  2. 停止 Cron 调度器
//
// 参数：
//   - _: 停止上下文（未使用，任务取消通过内部 cancel 函数实现）
//
// 注意事项：
//   - 不会等待正在执行的任务完成，立即触发取消
//   - 任务应该监听 context.Done() 并及时退出
func (s *server) Stop(_ context.Context) error {
	s.cancel()
	s.cron.stop()
	return nil
}
