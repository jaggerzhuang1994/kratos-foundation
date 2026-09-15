package output

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/DeRuina/timberjack"
	"github.com/go-kratos/kratos/v2/log"
)

// FileConfig 描述文件路径以及可选的轮转策略。
type FileConfig struct {
	Path     string
	Rotating *RotatingFileConfig
}

// NewFile 创建文件 Logger；轮转配置只影响文件管理，不改变日志编码。
func NewFile(config FileConfig) (log.Logger, func(), error) {
	if strings.TrimSpace(config.Path) == "" {
		return nil, nil, fmt.Errorf("file logger path is required")
	}

	// 轮转库在追加失败时可能重命名旧路径；目录不是合法日志文件，必须提前拒绝。
	if info, err := os.Stat(config.Path); err == nil && info.IsDir() {
		return nil, nil, fmt.Errorf("file logger path is a directory")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("stat file logger path: %w", err)
	}

	var writer io.WriteCloser
	if config.Rotating == nil {
		file, err := os.OpenFile(config.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
		if err != nil {
			return nil, nil, err
		}
		writer = file
	} else {
		// Close 同时等待压缩/保留任务退出，旧配置及未发布候选才能被完整释放。
		rotating := &timberjack.Logger{
			Filename:   config.Path,
			MaxSize:    config.Rotating.MaxSize,
			MaxAge:     config.Rotating.MaxFileAge,
			MaxBackups: config.Rotating.MaxFiles,
			LocalTime:  config.Rotating.LocalTime,
			Compress:   config.Rotating.Compress,
			FileMode:   0o600,
		}
		// 准备阶段只验证可追加，不启动轮转或保留任务；旧代仍可能正在写同一路径。
		if err := os.MkdirAll(filepath.Dir(config.Path), 0o755); err != nil {
			return nil, nil, fmt.Errorf("prepare log directory: %w", err)
		}
		probe, err := os.OpenFile(config.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("prepare log file: %w", err)
		}
		if err := probe.Close(); err != nil {
			return nil, nil, fmt.Errorf("close prepared log file: %w", err)
		}

		writer = rotating
	}
	guardedWriter := &fileWriter{writer: writer}
	return log.NewStdLogger(guardedWriter), newFileLoggerCleanup(guardedWriter), nil
}

// RotatingFileConfig 描述文件轮转和保留策略。
type RotatingFileConfig struct {
	// MaxSize 是触发轮转的文件大小上限，单位为 MB。
	MaxSize int
	// MaxFileAge 是旧日志文件的最长保留天数；0 表示不限制。
	MaxFileAge int
	// MaxFiles 是保留的旧日志文件数量上限；0 表示不限制。
	MaxFiles int
	// LocalTime 控制备份文件名中的时间戳是否使用本地时间。
	LocalTime bool
	// Compress 控制是否使用 gzip 压缩轮转文件。
	Compress bool
}

type fileWriter struct {
	mu     sync.Mutex
	writer io.WriteCloser
	closed bool
}

// Write 与 Close 串行化，cleanup 后禁止继续访问文件。
func (w *fileWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	return w.writer.Write(data)
}

// Close 永久关闭写入边界；即使底层关闭失败，后续写入也不会重新打开文件。
func (w *fileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.writer.Close()
}

// newFileLoggerCleanup 返回幂等关闭函数；cleanup 无错误返回值，因此关闭失败必须明确写入标准错误。
func newFileLoggerCleanup(closer io.Closer) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			if err := closer.Close(); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "close file logger: %v\n", err)
			}
		})
	}
}
