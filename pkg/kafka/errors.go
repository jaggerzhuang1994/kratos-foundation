package kafka

import (
	"errors"
	"fmt"
)

type permanentError struct {
	// err 标记为不可重试的原始业务错误。
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

// BatchFailure 标识一次批量发布中失败的单个输入。
type BatchFailure struct {
	// Index 失败消息在原始批次中的零基下标。
	Index int
	// MessageID 失败消息的标识。
	MessageID string
	// Err 该消息发送失败的原因。
	Err error
}

// BatchError 报告一次批量发布中的逐条失败。
type BatchError struct {
	// Failures 批次中失败的消息及错误，不代表整个批次均未发送。
	Failures []BatchFailure
}

// Error 汇总批量发布中的失败消息数量。
func (e *BatchError) Error() string {
	if e == nil {
		return "queue batch publish failed"
	}
	return fmt.Sprintf(
		"queue batch publish failed for %d message(s)",
		len(e.Failures),
	)
}

// Unwrap 暴露每条非 nil 失败原因，使 errors.Is 和 errors.As 可匹配其中任意一条。
func (e *BatchError) Unwrap() []error {
	if e == nil {
		return nil
	}
	errs := make([]error, 0, len(e.Failures))
	for _, failure := range e.Failures {
		if failure.Err != nil {
			errs = append(errs, failure.Err)
		}
	}
	return errs
}
