package errors

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const mdLegacyHTTPHeaderKey = "http_header"

// statusError 通过公开状态访问器兼容旧生成器，无需依赖其运行库。
type statusError interface {
	error
	GetCode() int32
	GetReason() string
	GetMessage() string
	GetMetadata() map[string]string
}

// localStatus 按由外到内的顺序读取语义，避免 cause 中的状态覆盖调用方明确指定的错误。
func localStatus(err error) *Error {
	if err == nil {
		return nil
	}
	if se, ok := err.(*Error); ok {
		return se
	}
	if se, ok := err.(statusError); ok {
		converted := New(int(se.GetCode()), se.GetReason(), se.GetMessage()).WithMetadata(se.GetMetadata()).WithCause(err)
		// 从原始 JSON 读取数据，避免旧 HttpData 方法将大整数转换为 float64。
		if raw := converted.Metadata[mdHTTPDataKey]; json.Valid([]byte(raw)) {
			converted = converted.WithHTTPData(json.RawMessage(raw))
		}
		if raw := converted.Metadata[mdLegacyHTTPHeaderKey]; raw != "" {
			var legacy map[string]string
			if json.Unmarshal([]byte(raw), &legacy) == nil {
				headers := make(http.Header, len(legacy))
				for key, value := range legacy {
					headers.Set(key, value)
				}
				converted = converted.WithHTTPHeaders(headers)
			}
		}
		delete(converted.Metadata, mdLegacyHTTPHeaderKey)
		return converted
	}
	// 外层已明确声明 gRPC 状态时，不采用其内部 cause 的业务状态。
	if _, ok := err.(interface{ GRPCStatus() *status.Status }); ok {
		return nil
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return localStatus(wrapped.Unwrap())
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if se := localStatus(child); se != nil {
				return se
			}
		}
	}
	return nil
}

// Normalize 在服务端传输边界归一化错误，保留明确协议语义并隐藏未知故障的内部文本。
func Normalize(err error) error {
	if err == nil {
		return nil
	}
	if se := localStatus(err); se != nil {
		if se.Code >= 500 {
			if stderrors.Is(err, context.Canceled) {
				return New(499, "CLIENT_CANCELED", "Client Closed Request").WithCause(err)
			}
			if stderrors.Is(err, context.DeadlineExceeded) {
				return New(http.StatusGatewayTimeout, "DEADLINE_EXCEEDED", "Gateway Timeout").WithCause(err)
			}
		}
		if se.Code >= 400 && se.Code <= 599 {
			if se == err {
				return se
			}
			return se.WithCause(err)
		}
		return unknownError(err)
	}
	if stderrors.Is(err, context.Canceled) {
		return New(499, "CLIENT_CANCELED", "Client Closed Request").WithCause(err)
	}
	if stderrors.Is(err, context.DeadlineExceeded) {
		return New(http.StatusGatewayTimeout, "DEADLINE_EXCEEDED", "Gateway Timeout").WithCause(err)
	}
	if gs, ok := status.FromError(err); ok {
		se := FromError(err)

		// 无结构化详情的服务端状态可能直接包含 driver 原文，不能公开。
		explicit := false
		for _, detail := range gs.Details() {
			if _, ok := detail.(*errdetails.ErrorInfo); ok {
				explicit = true
				break
			}
		}
		if explicit {
			return se.WithCause(err)
		}
		switch gs.Code() {
		case codes.Canceled:
			return New(499, "CLIENT_CANCELED", "Client Closed Request").WithCause(err)
		case codes.DeadlineExceeded:
			return New(http.StatusGatewayTimeout, "DEADLINE_EXCEEDED", "Gateway Timeout").WithCause(err)
		}
		if se.Code >= 400 && se.Code < 500 {
			return se.WithCause(err)
		}
	}
	return unknownError(err)
}

// unknownError 仅向调用方返回稳定消息，原始诊断保留在本地因果链。
func unknownError(err error) *Error {
	return New(http.StatusInternalServerError, "UNKNOWN", http.StatusText(http.StatusInternalServerError)).WithCause(err).WithErrStack()
}
