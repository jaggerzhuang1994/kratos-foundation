package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
)

// Update 携带一次完整有序输入或配置源错误。
type Update struct {
	Values   []*kratosconfig.KeyValue
	Err      error
	Terminal bool
}

type sourceUpdate struct {
	index int
	Update
}

// Stream 将子 watcher 的变更通知转换成完整有序配置输入。
type Stream struct {
	ctx     context.Context
	cancel  context.CancelFunc
	updates chan sourceUpdate

	sources   []kratosconfig.Source
	watchers  []kratosconfig.Watcher
	cached    [][]*kratosconfig.KeyValue
	workers   sync.WaitGroup
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
}

// Next 必须顺序调用；由这个唯一汇总者更新缓存并发布完整快照，防止跨源更新倒退。
func (s *Stream) Next() Update {
	if err := s.ctx.Err(); err != nil {
		return Update{Err: err, Terminal: true}
	}
	select {
	case update := <-s.updates:
		if update.Terminal {
			s.cancel()
		}
		if update.Err != nil {
			return update.Update
		}
		s.cached[update.index] = update.Values
		return Update{Values: s.snapshot()}
	case <-s.ctx.Done():
		return Update{Err: s.ctx.Err(), Terminal: true}
	}
}

func (s *Stream) watch(index int, sourceWatcher kratosconfig.Watcher) {
	defer s.workers.Done()
	for {
		_, err := sourceWatcher.Next()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			s.send(index, Update{Err: fmt.Errorf("watch config source %d: %w", index, err), Terminal: true})
			return
		}

		var values []*kratosconfig.KeyValue
		for attempt := 0; ; attempt++ {
			if s.ctx.Err() != nil {
				return
			}
			values, err = s.sources[index].Load()
			if err == nil {
				break
			}
			if !s.send(index, Update{Err: fmt.Errorf("reload config source %d: %w", index, err)}) {
				return
			}
			if !reconnect.Transient(err) && !errors.Is(err, os.ErrNotExist) {
				break
			}
			if waitErr := (reconnect.Backoff{}).Wait(s.ctx, attempt); waitErr != nil {
				return
			}
		}
		if err != nil {
			continue
		}
		// 发送前脱离 Source 的可变缓冲；下一次 Load 可以与汇总者并行。
		if !s.send(index, Update{Values: cloneKeyValues(values)}) {
			return
		}
	}
}

func (s *Stream) send(index int, update Update) bool {
	select {
	case s.updates <- sourceUpdate{index: index, Update: update}:
		return true
	case <-s.ctx.Done():
		return false
	}
}

func (s *Stream) snapshot() []*kratosconfig.KeyValue {
	var result []*kratosconfig.KeyValue
	for _, values := range s.cached {
		result = append(result, cloneKeyValues(values)...)
	}
	return result
}

// Close 幂等停止全部子 watcher，并等待内部 worker 退出。
func (s *Stream) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.closeErr = stopWatchers(s.watchers)
		s.workers.Wait()
		close(s.closeDone)
	})
	<-s.closeDone
	return s.closeErr
}
