package utils

import "sync"

// GoroutinePool 协程池
type GoroutinePool struct {
	wg  sync.WaitGroup
	sem chan struct{}
}

// NewGoroutinePool 新建协程池
func NewGoroutinePool(max int) *GoroutinePool {
	return &GoroutinePool{
		sem: make(chan struct{}, max),
	}
}

// Go 异步执行方法
func (wg *GoroutinePool) Go(f func()) {
	wg.sem <- struct{}{} // 占一个坑
	wg.wg.Add(1)
	go func() {
		defer func() { <-wg.sem }() // 任务结束，释放
		defer wg.wg.Done()
		f()
	}()
}

// Wait 等待所有任务完成
func (wg *GoroutinePool) Wait() {
	wg.wg.Wait()
}
