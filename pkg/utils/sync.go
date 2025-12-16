package common

import "sync"

type GoroutinePool struct {
	wg  sync.WaitGroup
	sem chan struct{}
}

func NewGoroutinePool(max int) *GoroutinePool {
	return &GoroutinePool{
		sem: make(chan struct{}, max),
	}
}

func (wg *GoroutinePool) Go(f func()) {
	wg.sem <- struct{}{} // 占一个坑
	wg.wg.Go(func() {
		defer func() { <-wg.sem }() // 任务结束，释放
		f()
	})
}

func (wg *GoroutinePool) Wait() {
	wg.wg.Wait()
}
