package utils

import (
	"sync"
)

// GoroutinePool 协程池，用于限制并发执行的 goroutine 数量
// 通过带缓冲的 channel 实现信号量机制
type GoroutinePool struct {
	wg  sync.WaitGroup // 等待组，用于等待所有任务完成
	sem chan struct{}  // 信号量，用于控制并发数量
}

// NewGoroutinePool 创建一个新的协程池
// max 参数指定最大并发 goroutine 数量
func NewGoroutinePool(max int) *GoroutinePool {
	return &GoroutinePool{
		sem: make(chan struct{}, max),
	}
}

// Go 异步执行函数
// 如果协程池已满，会阻塞直到有可用的 goroutine
// 必须调用 Wait 等待所有任务完成
func (wg *GoroutinePool) Go(f func()) {
	wg.sem <- struct{}{} // 占一个坑（阻塞直到有可用位置）
	wg.wg.Add(1)
	go func() {
		defer func() { <-wg.sem }() // 任务结束，释放占用的坑位
		defer wg.wg.Done()
		f()
	}()
}

// Wait 等待所有任务完成
// 会阻塞直到所有通过 Go 提交的任务都执行完毕
func (wg *GoroutinePool) Wait() {
	wg.wg.Wait()
}
