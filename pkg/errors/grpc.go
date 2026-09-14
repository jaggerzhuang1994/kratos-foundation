package errors

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-kratos/kratos/v2/errors"
	httpstatus "github.com/go-kratos/kratos/v2/transport/http/status"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// grpcMetadata 为服务间调用保留业务状态和完整诊断，公开出口由网关过滤。
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
	// 堆栈包含本地 cause 诊断，接收端可继续包装并转发；HTTP 响应头不随之传输。
	if stack := e.ErrStack(); stack != "" {
		md[mdErrStackKey] = stack
	}
	// HTTP 网关需要跨 gRPC 恢复业务 data，仅发送可编码的快照。
	if e.httpData != nil {
		encoded, err := json.Marshal(e.httpData)
		if err == nil {
			md[mdHTTPDataKey] = string(encoded)
		}
	}
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
	if se := localStatus(err); se != nil {
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
			ret = ret.WithMetadata(d.Metadata)
			// 保留远端堆栈供内部日志和后续转发，仅清除响应头。
			delete(ret.Metadata, mdHTTPHeadersKey)
			delete(ret.Metadata, mdLegacyHTTPHeaderKey)
			// UseNumber 保留大整数精度；无效远端 data 不影响原有错误码和 reason。
			raw := d.Metadata[mdHTTPDataKey]
			if json.Valid([]byte(raw)) {
				var data any
				decoder := json.NewDecoder(strings.NewReader(raw))
				decoder.UseNumber()
				if err := decoder.Decode(&data); err == nil {
					ret = ret.WithHTTPData(data)
				}
			}
			return ret
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
