package errors

import (
	"encoding/json"
	"fmt"
	"io"
	http2 "net/http"
	"strconv"
	"strings"

	"github.com/go-kratos/kratos/v2/errors"
	httpstatus "github.com/go-kratos/kratos/v2/transport/http/status"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

const (
	mdErrStackKey        = "err_stack"
	mdReasonCodeKey      = "reason_code"
	mdHttpDataKey        = "http_data"
	mdHttpHeadersKey     = "http_headers"
	mdHttpResponse       = "http_response"
	mdValidationErrorKey = "validation_error"
)

// 格式化err 忽略的md key
var formatExcludeMdKeys = []string{
	mdErrStackKey, mdReasonCodeKey,
}

// Error is a status error.
type Error struct {
	Status
	cause error
}

func (e *Error) Error() string {
	return fmt.Sprintf("error: code=%d Reason=%s reason_code=%d message=%s metadata=%v", e.Code, e.Reason, e.ReasonCode(), e.Message, e.getFormatMd())
}

func (e *Error) getFormatMd() map[string]string {
	md := make(map[string]string, len(e.Metadata))
	for k, v := range e.Metadata {
		if !utils.Includes(formatExcludeMdKeys, k) {
			md[k] = v
		}
	}
	return md
}

// Unwrap provides compatibility for Go 1.13 error chains.
func (e *Error) Unwrap() error { return e.cause }

// Is matches each error in the chain with the target value.
func (e *Error) Is(err error) bool {
	if se := new(Error); errors.As(err, &se) {
		return se.Code == e.Code && se.Reason == e.Reason
	}
	if kse := new(KratosError); errors.As(err, &kse) {
		return kse.Code == e.Code && kse.Reason == e.Reason
	}
	return false
}

// WithCause with the underlying Cause of the error.
func (e *Error) WithCause(cause error) *Error {
	err := Clone(e)
	err.cause = cause
	return err
}

// WithMetadata merge with an MD formed by the mapping of key, value.
func (e *Error) WithMetadata(md map[string]string) *Error {
	err := Clone(e)
	// 合并 metadata
	for k, v := range md {
		err.Metadata[k] = v
	}
	return err
}

// GRPCStatus returns the Status represented by se.
func (e *Error) GRPCStatus() *status.Status {
	s, _ := status.New(httpstatus.ToGRPCCode(int(e.Code)), e.Message).
		WithDetails(&errdetails.ErrorInfo{
			Reason:   e.Reason,
			Metadata: e.Metadata,
		})
	return s
}

func (e *Error) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v':
		if s.Flag('+') {
			// error
			// err_stack
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

// WithErrStack 携带堆栈，栈顶是 WithErrStack 调用点
func (e *Error) WithErrStack(optionalSkip ...int) *Error {
	// 0 - runtime.Callers 的callers调用点
	// 1 - errors/stack.go 的runtime.Callers调用点
	// 2 - errors/errors.go 当前函数 callers(skip) 的调用点
	// 3 - 业务侧调用 WithErrStack 的调用点
	var skip = 3
	if len(optionalSkip) > 0 {
		skip = optionalSkip[0]
	}
	err := Clone(e)
	err.Metadata[mdErrStackKey] += fmt.Sprintf("%+v", callers(skip))
	return err
}

//// WithErrStackSkip 携带堆栈，并跳过几个栈顶
//func (e *Error) WithErrStackSkip(skip uint) *Error {
//	err := Clone(e)
//	err.Metadata[mdErrStackKey] += fmt.Sprintf("%+v", callers(3+int(skip)))
//	return err
//}

func (e *Error) ErrStack() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	if e.Metadata != nil && e.Metadata[mdErrStackKey] != "" {
		_, _ = fmt.Fprintf(&b, "%s\n", e.Metadata[mdErrStackKey])
	}
	if e.cause != nil {
		_, _ = fmt.Fprintf(&b, "Cause by: %+v", e.cause)
	}
	return b.String()
}

// WithReasonCode 带上reasonCode
func (e *Error) WithReasonCode(reasonCode int) *Error {
	err := Clone(e)
	err.Metadata[mdReasonCodeKey] = strconv.Itoa(reasonCode)
	return err
}

func (e *Error) ReasonCode() int {
	if e == nil || e.Metadata == nil || e.Metadata[mdReasonCodeKey] == "" {
		return int(e.Code)
	}
	reasonCode, convErr := strconv.Atoi(e.Metadata[mdReasonCodeKey])
	if convErr != nil {
		return int(e.Code)
	}
	return reasonCode
}

// WithHttpData 带上http渲染data
func (e *Error) WithHttpData(data any) *Error {
	err := Clone(e)
	jsonData, _ := json.Marshal(data)
	err.Metadata[mdHttpDataKey] = string(jsonData)
	return err
}

func (e *Error) HttpData() any {
	if e == nil || e.Metadata == nil || e.Metadata[mdHttpDataKey] == "" {
		return nil
	}
	var data any
	err := json.Unmarshal([]byte(e.Metadata[mdHttpDataKey]), &data)
	if err != nil {
		return nil
	}
	return data
}

func (e *Error) WithHttpHeaders(headers http2.Header) *Error {
	err := Clone(e)
	// 合并原来的header
	mergedHeaders := err.HttpHeaders()
	for k, v := range headers {
		for _, vv := range v {
			if !utils.Includes(mergedHeaders.Values(k), vv) {
				mergedHeaders.Add(k, vv)
			}
		}
	}
	jsonData, _ := json.Marshal(mergedHeaders)
	err.Metadata[mdHttpHeadersKey] = string(jsonData)
	return err
}

func (e *Error) HttpHeaders() http2.Header {
	if e == nil || e.Metadata == nil || e.Metadata[mdHttpHeadersKey] == "" {
		return http2.Header{}
	}
	headers := http2.Header{}
	_ = json.Unmarshal([]byte(e.Metadata[mdHttpHeadersKey]), &headers)
	return headers
}

// WithHttpResponse 带上http渲染body
func (e *Error) WithHttpResponse(response string) *Error {
	err := Clone(e)
	err.Metadata[mdHttpResponse] = response
	return err
}

func (e *Error) HttpResponse() string {
	if e == nil || e.Metadata == nil {
		return ""
	}
	return e.Metadata[mdHttpResponse]
}

func (e *Error) WithValidationError(validationError []*ValidationError) *Error {
	err := Clone(e)

	data, _ := json.Marshal(validationError)
	err.Metadata[mdValidationErrorKey] = string(data)
	return err
}

func (e *Error) ValidationError() []*ValidationError {
	if e == nil || e.Metadata == nil || e.Metadata[mdValidationErrorKey] == "" {
		return nil
	}

	var errs []*ValidationError
	_ = json.Unmarshal([]byte(e.Metadata[mdValidationErrorKey]), &errs)
	return errs
}

// New returns an error object for the code, message.
func New(code int, reason, message string) *Error {
	return &Error{
		Status: Status{
			Code:    int32(code),
			Message: message,
			Reason:  reason,
		},
	}
}

// Newf New(code fmt.Sprintf(format, a...))
func Newf(code int, reason, format string, a ...any) *Error {
	return New(code, reason, fmt.Sprintf(format, a...))
}

// Errorf returns an error object for the code, message and error info.
func Errorf(code int, reason, format string, a ...any) error {
	return New(code, reason, fmt.Sprintf(format, a...))
}

// Code returns the http code for an error.
// It supports wrapped errors.
func Code(err error) int {
	return int(FromError(err).GetCode())
}

// Reason returns the Reason for a particular error.
// It supports wrapped errors.
func Reason(err error) string {
	return FromError(err).GetReason()
}

// Message returns the message for a particular error.
// It supports wrapped errors.
func Message(err error) string {
	return FromError(err).GetMessage()
}

// ErrStack returns the err stack for a particular error.
// It supports wrapped errors.
func ErrStack(err error) string {
	return FromError(err).ErrStack()
}

// ReasonCode returns the Reason code for a particular error.
// It supports wrapped errors.
func ReasonCode(err error) int {
	return FromError(err).ReasonCode()
}

func HttpData(err error) any {
	return FromError(err).HttpData()
}

func HttpHeaders(err error) http2.Header {
	return FromError(err).HttpHeaders()
}

func HttpResponse(err error) string {
	return FromError(err).HttpResponse()
}

// Clone deep clone error to a new error.
func Clone(err *Error) *Error {
	if err == nil {
		return nil
	}
	metadata := make(map[string]string, len(err.Metadata))
	for k, v := range err.Metadata {
		metadata[k] = v
	}
	return &Error{
		cause: err.cause,
		Status: Status{
			Code:     err.Code,
			Reason:   err.Reason,
			Message:  err.Message,
			Metadata: metadata,
		},
	}
}

// FromError try to convert an error to *Error.
// It supports wrapped errors.
func FromError(err error) *Error {
	if err == nil {
		return nil
	}
	if se := new(Error); errors.As(err, &se) {
		return se
	}
	gs, ok := status.FromError(err)
	if !ok {
		return New(errors.UnknownCode, errors.UnknownReason, err.Error())
	}
	ret := New(
		httpstatus.FromGRPCCode(gs.Code()),
		errors.UnknownReason,
		gs.Message(),
	)
	for _, detail := range gs.Details() {
		switch d := detail.(type) {
		case *errdetails.ErrorInfo:
			ret.Reason = d.Reason
			return ret.WithMetadata(d.Metadata)
		}
	}
	return ret
}
