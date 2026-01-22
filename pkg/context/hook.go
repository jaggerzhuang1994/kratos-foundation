// Package context 提供上下文管理功能，包括 Context Hook 机制。
//
// Context Hook 允许在应用初始化时扩展 context，向所有请求上下文
// 注入自定义数据，例如请求 ID、用户信息、租户 ID 等。
//
// 使用示例：
//
//	contextHook.WithContext(func(ctx context.Context) context.Context {
//	    // 自动生成请求 ID
//	    requestID := uuid.New().String()
//	    return context.WithValue(ctx, "request_id", requestID)
//	})
//
// 这些 context 修改函数会在每次创建新的请求上下文时自动应用，
// 确保所有请求都包含必要的上下文信息。
package context

import (
	"context"
)

// Hook Context 钩子接口，用于向 context 注入自定义数据
//
// Context Hook 提供了一种机制，允许在应用初始化阶段注册
// context 修改函数，这些函数会在每个请求的 context 创建时自动应用。
//
// 典型使用场景：
//   - 自动生成和注入请求 ID（trace ID）
//   - 从 HTTP header 中提取用户信息并注入 context
//   - 注入租户 ID 用于多租户隔离
//   - 注入请求元数据（来源、版本等）
//   - 设置默认的超时时间
//
// 执行顺序：
//
//	所有注册的 context 修改函数按照注册顺序（FIFO）依次执行，
//	每个函数接收前一个函数的返回值作为输入。
//
// 注意：
//   - context 修改函数应该是幂等的，多次调用应该产生相同的结果
//   - 避免在 context 中存储大量数据，context 会随请求传递
//   - 使用 context.Value 存储的值应该在请求级别共享
type Hook interface {
	// WithContext 注册一个 context 修改函数
	//
	// 该函数接收当前 context 并返回修改后的 context。
	// 函数可以添加新的键值对、修改现有值或包装 context。
	//
	// 参数：
	//   fn: context 修改函数，签名为 func(context.Context) context.Context
	//
	// 示例：
	//   // 注入请求 ID
	//   hook.WithContext(func(ctx context.Context) context.Context {
	//       requestID := uuid.New().String()
	//       return context.WithValue(ctx, "request_id", requestID)
	//   })
	//
	//   // 注入用户信息（从 token 中解析）
	//   hook.WithContext(func(ctx context.Context) context.Context {
	//       userID := getUserIDFromToken(ctx)
	//       return context.WithValue(ctx, "user_id", userID)
	//   })
	//
	//   // 设置默认超时
	//   hook.WithContext(func(ctx context.Context) context.Context {
	//       _, cancel := context.WithTimeout(ctx, 30*time.Second)
	//       // 注意：不要在这里调用 cancel，让调用者负责
	//       return ctx
	//   })
	//
	// 注意事项：
	//   - 避免循环修改 context
	//   - 不要在函数中阻塞或执行耗时操作
	//   - 确保传递的 context 不会被意外取消
	WithContext(fn func(ctx context.Context) context.Context)

	// Chain 执行所有注册的 context 修改函数，形成处理链
	//
	// 此方法按照注册顺序依次执行所有 context 修改函数，
	// 每个函数接收前一个函数的返回值作为输入，最终返回修改后的 context。
	//
	// 参数：
	//   ctx: 原始 context，通常是请求的根 context
	//
	// 返回：
	//   context.Context: 经过所有修改函数处理后的 context
	//
	// 执行流程：
	//   1. 从输入的 ctx 开始
	//   2. 按照注册顺序依次调用每个修改函数
	//   3. 每个函数接收上一个函数的返回值
	//   4. 返回最终的 context
	//
	// 示例：
	//   // 假设注册了三个修改函数
	//   hook.WithContext(addRequestID)
	//   hook.WithContext(addUserID)
	//   hook.WithContext(addTenantID)
	//
	//   // 使用 Chain 执行所有修改
	//   ctx := hook.Chain(context.Background())
	//   // ctx 现在包含 request_id、user_id 和 tenant_id
	//
	// 注意：
	//   - 函数按照注册顺序执行，后注册的可以覆盖前面的修改
	//   - 如果某个函数返回 nil，会导致后续函数执行时 panic
	//   - 建议每个函数专注于单一职责，保持逻辑简单
	//   - 避免在函数中执行耗时操作，会影响每个请求的性能
	//
	// 性能考虑：
	//   此方法会在每个请求中被调用，因此：
	//   - 建议控制注册的函数数量（建议不超过 10 个）
	//   - 避免在函数中进行复杂的计算或 I/O 操作
	//   - 函数应该是幂等的，多次调用产生相同结果
	Chain(context.Context) context.Context
}

// hook Hook 接口的实现，持有所有 context 修改函数
//
// 多个 context 修改函数按照注册顺序存储，
// 在创建新 context 时依次调用，形成处理链。
type hook struct {
	// context 修改函数列表，按注册顺序存储
	// 每个函数接收前一个函数的输出作为输入
	withContext []func(context.Context) context.Context
}

// NewHook 创建一个新的 Context 钩子实例
//
// 返回的 Hook 实例可以在应用初始化阶段使用，
// 注册 context 修改函数以增强所有请求的上下文。
func NewHook() Hook {
	return &hook{}
}

// WithContext 添加一个 context 修改函数到钩子中
//
// 这些函数将在 NewContext 中按注册顺序依次执行。
//
// 执行流程：
//  1. 从原始 context 开始
//  2. 依次调用每个注册的修改函数
//  3. 每个函数接收上一个函数的返回值
//  4. 返回最终的 context
//
// 示例：
//
//	// 注册三个修改函数
//	hook.WithContext(addRequestID)
//	hook.WithContext(addUserID)
//	hook.WithContext(addTenantID)
//
//	// 执行顺序：original -> addRequestID -> addUserID -> addTenantID -> final
//
// 注意：
//   - 函数按照注册顺序执行，后注册的函数可以覆盖前面的修改
//   - 如果某个函数返回 nil，后续函数将无法执行
//   - 建议每个函数专注于单一职责
func (h *hook) WithContext(withContext func(context.Context) context.Context) {
	h.withContext = append(h.withContext, withContext)
}

// Chain 执行所有注册的 context 修改函数
//
// 实现 Hook 接口的 Chain 方法。
// 按照注册顺序依次执行所有 context 修改函数，形成处理链。
//
// 参数：
//   ctx: 原始 context，通常是请求的根 context
//
// 返回：
//   context.Context: 经过所有修改函数处理后的 context
//
// 执行示例：
//   // 假设注册了以下函数：
//   // 1. addRequestID: 添加 request_id
//   // 2. addUserID: 添加 user_id
//   // 3. addTenantID: 添加 tenant_id
//
//   ctx := hook.Chain(context.Background())
//   // 执行流程：
//   // context.Background() -> addRequestID() -> addUserID() -> addTenantID() -> final ctx
//
// 注意事项：
//   - 此方法会在每个请求中被调用，性能敏感
//   - 如果某个函数返回 nil，会导致后续函数 panic
//   - 建议在注册时确保所有函数都不会返回 nil
//   - 函数应该快速执行，避免阻塞请求处理
//
// 性能优化建议：
//   - 控制注册的函数数量，建议不超过 10 个
//   - 避免在函数中进行复杂计算或 I/O 操作
//   - 可以考虑缓存不变的计算结果
func (h *hook) Chain(ctx context.Context) context.Context {
	for _, withCtx := range h.withContext {
		ctx = withCtx(ctx)
	}
	return ctx
}
