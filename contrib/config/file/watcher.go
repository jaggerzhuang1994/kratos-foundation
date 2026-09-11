package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	kratosconfig "github.com/go-kratos/kratos/v2/config"
	kratosfile "github.com/go-kratos/kratos/v2/config/file"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
)

type fileSource struct {
	kratosconfig.Source
	path string
}

func newFileSource(path string) (*fileSource, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return &fileSource{Source: kratosfile.NewSource(absolute), path: absolute}, nil
}

// Watch 监听稳定的父目录，避免文件被原子替换后仍然监听旧 inode。
func (s *fileSource) Watch() (kratosconfig.Watcher, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		return nil, err
	}
	linkInfo, err := os.Lstat(s.path)
	if err != nil {
		return nil, err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := &fileWatcher{
		source: s, notifications: watcher, directory: info.IsDir(),
		symlink: linkInfo.Mode()&os.ModeSymlink != 0, ctx: ctx, cancel: cancel,
	}
	if err := result.bind(); err != nil {
		return nil, errors.Join(err, result.Stop())
	}
	return result, nil
}

type fileWatcher struct {
	source        *fileSource
	notifications *fsnotify.Watcher
	directory     bool
	symlink       bool
	target        string
	ctx           context.Context
	cancel        context.CancelFunc
	stopOnce      sync.Once
	stopErr       error
}

// FullSnapshot 声明每次成功的 Next 返回本源完整状态；空结果表示全部删除。
// 返回值至少在下一次 Next 前保持稳定，Stream 会复制后发布，不再重复 Load。
func (w *fileWatcher) FullSnapshot() bool { return true }

func (w *fileWatcher) Next() ([]*kratosconfig.KeyValue, error) {
	for {
		if err := w.ctx.Err(); err != nil {
			return nil, err
		}
		select {
		case <-w.ctx.Done():
			return nil, w.ctx.Err()
		case err, open := <-w.notifications.Errors:
			if !open {
				return nil, context.Canceled
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				// 丢事件时重新取完整快照，避免等待一个永远不会补发的变更。
				return w.reload()
			}
			return nil, err
		case event, open := <-w.notifications.Events:
			if !open {
				return nil, context.Canceled
			}
			parent := filepath.Dir(event.Name)
			if event.Name == w.source.path || event.Name == w.target || event.Name == filepath.Dir(w.source.path) ||
				(w.directory && (parent == w.source.path || parent == w.target)) ||
				(w.symlink && parent == filepath.Dir(w.source.path)) {
				if event.Op&(fsnotify.Rename|fsnotify.Remove) != 0 {
					if err := w.removeReplaced(event.Name); err != nil {
						return nil, err
					}
				}
				return w.reload()
			}
		}
	}
}

func (w *fileWatcher) reload() ([]*kratosconfig.KeyValue, error) {
	var backoff reconnect.Backoff
	for attempt := 0; ; attempt++ {
		if err := w.ctx.Err(); err != nil {
			return nil, err
		}
		err := w.bind()
		if err == nil {
			var values []*kratosconfig.KeyValue
			values, err = w.source.Load()
			if err == nil {
				return values, nil
			}
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("reload file source %q: %w", w.source.path, err)
		}
		// 临时移走文件时保留旧快照，等待重建；Stop 能立即结束等待。
		if err := backoff.Wait(w.ctx, attempt); err != nil {
			return nil, err
		}
	}
}

// bind 在发布快照前绑定文件，避免 kqueue 发出替换通知后尚未重绑叶子的窗口。
func (w *fileWatcher) bind() error {
	// kqueue 的事件名称使用真实目标路径；显式绑定该路径，兼容目标写入和链接切换。
	target := w.source.path
	if w.symlink {
		var err error
		target, err = filepath.EvalSymlinks(w.source.path)
		if err != nil {
			return err
		}
	}
	if w.target != "" && w.target != target {
		if err := w.removeReplaced(w.target); err != nil {
			return err
		}
	}
	w.target = target
	for _, path := range []string{filepath.Dir(w.source.path), w.target} {
		if err := w.notifications.Add(path); err != nil {
			return err
		}
	}
	if !w.directory {
		return nil
	}
	entries, err := os.ReadDir(w.target)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if err := w.notifications.Add(filepath.Join(w.target, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// removeReplaced 移除旧 inode 及其显式叶子监听，再由 bind 绑定新路径。
func (w *fileWatcher) removeReplaced(path string) error {
	for _, watched := range w.notifications.WatchList() {
		if watched != path && !strings.HasPrefix(watched, path+string(filepath.Separator)) {
			continue
		}
		if err := w.notifications.Remove(watched); err != nil && !errors.Is(err, fsnotify.ErrNonExistentWatch) {
			return err
		}
	}
	return nil
}

func (w *fileWatcher) Stop() error {
	w.stopOnce.Do(func() {
		w.cancel()
		w.stopErr = w.notifications.Close()
	})
	return w.stopErr
}
