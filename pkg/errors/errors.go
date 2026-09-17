package errors

import (
	"fmt"
	"io"
	"maps"
	"runtime"

	"github.com/go-kratos/kratos/v2/errors"
)

const (
	mdErrStackKey        = "err_stack"
	mdReasonCodeKey      = "reason_code"
	mdHTTPCodeKey        = "http_code"
	mdHTTPDataKey        = "http_data"
	mdHTTPHeadersKey     = "http_headers"
	mdValidationErrorKey = "validation_error"
)

// Error 为 Kratos 状态错误提供业务错误与 HTTP 响应扩展。
type Error struct {
	// Status 承载 Kratos 状态码、原因、公开消息及元数据。
	errors.Status
	// cause 保留原始错误链，由 Unwrap 返回。
	cause error
	// httpData 保存 HTTP 响应附加数据；链式错误副本内部共享，存取时对可 JSON 编码值复制。
	// 无法编码的值保留原引用，由响应编码器报错。
	httpData any
}

// Error 格式化可公开状态与元数据，不泄漏栈和传输层私有字段。
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("error: code=%d Reason=%s reason_code=%d message=%s metadata=%v", e.Code, e.Reason, e.ReasonCode(), e.Message, e.PublicMetadata())
}

// Unwrap 返回底层原因，使标准 errors.Is 与 errors.As 能遍历因果链。
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Is 按状态码和原因匹配本包或 Kratos 的状态错误。
func (e *Error) Is(err error) bool {
	if e == nil {
		return err == nil
	}
	if se := new(Error); errors.As(err, &se) {
		return se.Code == e.Code && se.Reason == e.Reason
	}
	if kse := new(errors.Error); errors.As(err, &kse) {
		return kse.Code == e.Code && kse.Reason == e.Reason
	}
	return false
}

// WithCause 返回携带底层原因的错误副本。
func (e *Error) WithCause(cause error) *Error {
	err := clone(e)
	err.cause = cause
	return err
}

// Format 支持紧凑格式，以及通过 %+v 输出栈和底层原因。
func (e *Error) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v':
		if s.Flag('+') {
			// 栈仅在调用方明确请求 %+v 时输出，避免普通日志重复打印大量帧。
			_, _ = fmt.Fprintf(s, "%s", e.Error())
			_, _ = fmt.Fprintf(s, "%s", e.ErrStack())
			return
		}
		fallthrough
	case 's':
		_, _ = io.WriteString(s, e.Error())
	case 'q':
		_, _ = fmt.Fprintf(s, "%q", e.Error())
	}
}

// New 使用 HTTP 状态码、原因和公开消息创建业务错误。
func New(code int, reason, message string) *Error {
	return &Error{
		Status: errors.Status{
			Code:    int32(code),
			Message: message,
			Reason:  reason,
		},
	}
}

// Code 返回错误链中的 HTTP 状态码；nil 表示成功。
func Code(err error) int {
	if err == nil {
		return 200
	}
	return int(FromError(err).GetCode())
}

// Reason 返回错误链中的稳定原因标识。
func Reason(err error) string {
	if err == nil {
		return errors.UnknownReason
	}
	return FromError(err).GetReason()
}

// Message 返回错误链中的公开消息。
func Message(err error) string {
	if err == nil {
		return ""
	}
	return FromError(err).GetMessage()
}

// clone 深复制可变元数据，保证链式 With 方法不会修改原错误。
func clone(err *Error) *Error {
	if err == nil {
		return nil
	}
	metadata := make(map[string]string, len(err.Metadata))
	maps.Copy(metadata, err.Metadata)
	return &Error{
		cause:    err.cause,
		httpData: err.httpData,
		Status: errors.Status{
			Code:     err.Code,
			Reason:   err.Reason,
			Message:  err.Message,
			Metadata: metadata,
		},
	}
}

// SupportPackageIsVersion1 供生成的错误辅助代码断言本包兼容版本。
const SupportPackageIsVersion1 = errors.SupportPackageIsVersion1

// stack 保存一组程序计数器，用于按需恢复调用帧。
type stack []uintptr

// Format 仅在 %+v 下展开调用帧，普通错误字符串保持紧凑。
func (s *stack) Format(st fmt.State, verb rune) {
	switch verb {
	case 'v':
		if !st.Flag('+') {
			return
		}
		frames := runtime.CallersFrames(*s)
		for {
			frame, more := frames.Next()
			_, _ = fmt.Fprintf(
				st,
				"\n%s\n\t%s:%d",
				frame.Function,
				frame.File,
				frame.Line,
			)
			if !more {
				return
			}
		}
	}
}

// callers 捕获有限深度的调用栈，避免错误对象无限增长。
func callers(skip int) *stack {
	const depth = 32
	var pcs [depth]uintptr
	n := runtime.Callers(skip, pcs[:])
	var st stack = pcs[0:n]
	return &st
}
