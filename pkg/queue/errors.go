package queue

import (
	"errors"
	"fmt"
)

type permanentError struct {
	err error
}

// Error 返回永久队列错误及其原因的文本。
func (e *permanentError) Error() string {
	if e == nil {
		return "permanent queue error"
	}
	return fmt.Sprintf("permanent queue error: %v", e.err)
}

// Unwrap 返回底层 Handler 错误，使 errors.Is 和 errors.As 可继续遍历。
func (e *permanentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// Permanent 将 Handler 错误标记为不可重试；nil 仍返回 nil。
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent 判断错误链中是否存在 Permanent 标记。
func IsPermanent(err error) bool {
	var target *permanentError
	return errors.As(err, &target)
}
