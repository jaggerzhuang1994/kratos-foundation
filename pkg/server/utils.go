// Package server 提供通用的泛型工具类型
//
// 该文件定义了一个泛型切片包装器，用于在依赖注入中
// 提供类型安全的链式调用接口。
//
// 主要功能：
//   - 类型安全的切片操作
//   - 链式调用支持
//   - 依赖注入友好的接口设计
package server

// SliceT 泛型切片接口
//
// 该接口定义了切片的基本操作，支持链式调用：
//   - Add: 添加元素到切片，返回接口本身以支持链式调用
//   - Get: 获取底层的切片数据
//
// 类型参数：
//   - T: 切片中元素的类型
//
// 使用场景：
//   - 在依赖注入中构建中间件链
//   - 构建服务器选项列表
//   - 任何需要类型安全切片操作的场景
//
// 使用示例：
//
//	// 创建中间件链
//	middlewares := NewMiddlewares(...)
//	middlewares.Add(middleware1).Add(middleware2)
//
//	// 获取切片数据
//	ms := middlewares.Get()
//	for _, m := range ms {
//	    // 使用中间件
//	}
type SliceT[T any] interface {
	Add(T) SliceT[T] // 添加元素并返回接口，支持链式调用
	Get() []T        // 获取底层切片数据
}

// sliceT 泛型切片的实现
//
// 该结构体是 SliceT 接口的默认实现，通过封装
// Go 的内置切片类型来实现接口功能。
//
// 类型参数：
//   - T: 切片中元素的类型
type sliceT[T any] []T

// Add 添加元素到切片
//
// 该方法将元素追加到切片末尾，并返回接口本身，
// 以支持链式调用。
//
// 参数说明：
//   - m: 要添加的元素
//
// 返回：
//   - SliceT[T]: 返回接口本身，支持链式调用
//
// 使用示例：
//
//	opts := new(sliceT[http.ServerOption])
//	opts.Add(opt1).Add(opt2).Add(opt3)
func (s *sliceT[T]) Add(m T) SliceT[T] {
	*s = append(*s, m)
	return s
}

// Get 获取底层切片数据
//
// 该方法返回底层切片的引用，可以直接访问和操作切片元素。
//
// 返回：
//   - []T: 底层切片的引用
//
// 注意事项：
//   - 返回的是切片的引用，修改会影响原始数据
//   - 该方法通常用于将切片传递给其他函数
//
// 使用示例：
//
//	opts := NewHttpServerOptions(...)
//	http.NewServer(opts.Get()...)
func (s *sliceT[T]) Get() []T {
	return *s
}
