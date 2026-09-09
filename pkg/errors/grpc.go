package errors

import (
	"net/http"
	"strconv"

	"github.com/go-kratos/kratos/v2/errors"
	httpstatus "github.com/go-kratos/kratos/v2/transport/http/status"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// grpcMetadata 只加入跨 gRPC 边界仍有业务意义的私有字段，并携带原始 HTTP 状态码。
func (e *Error) grpcMetadata() map[string]string {
	md := e.PublicMetadata()
	if md == nil {
		md = make(map[string]string, 3)
	}
	for _, key := range []string{mdReasonCodeKey, mdValidationErrorKey} {
		if value := e.Metadata[key]; value != "" {
			md[key] = value
		}
	}
	// 必须以状态字段为准，避免调用方通过同名 metadata 伪造传输状态码。
	md[mdHTTPCodeKey] = strconv.FormatInt(int64(e.Code), 10)
	return md
}

// GRPCStatus 把错误转换为 gRPC 状态，并只携带允许跨边界的元数据。
func (e *Error) GRPCStatus() *status.Status {
	s, _ := status.New(httpstatus.ToGRPCCode(int(e.Code)), e.Message).
		WithDetails(&errdetails.ErrorInfo{
			Reason:   e.Reason,
			Metadata: e.grpcMetadata(),
		})
	return s
}

// FromError 从普通错误、包装链或 gRPC 状态恢复本包错误。
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
			ret.Code = restoreUnknownHTTPCode(gs.Code(), d.Metadata, ret.Code)
			return ret.WithMetadata(d.Metadata)
		}
	}
	return ret
}

// restoreUnknownHTTPCode 在标准映射丢失状态码时，从可信范围内的传输元数据恢复原始值。
func restoreUnknownHTTPCode(grpcCode codes.Code, metadata map[string]string, fallback int32) int32 {
	if grpcCode != codes.Unknown {
		return fallback
	}
	parsed, err := strconv.ParseInt(metadata[mdHTTPCodeKey], 10, 32)
	if err != nil {
		return fallback
	}
	code := int(parsed)
	if code < http.StatusBadRequest || code > 599 {
		return fallback
	}
	// 拒绝与外层 gRPC status 不一致的 metadata，例如 Unknown 搭配 http_code=404。
	if httpstatus.ToGRPCCode(code) != codes.Unknown {
		return fallback
	}
	return int32(code)
}
