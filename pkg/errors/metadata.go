package errors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// PublicMetadata 返回可对外暴露的元数据副本，并过滤框架私有字段。
func (e *Error) PublicMetadata() map[string]string {
	if e == nil {
		return nil
	}
	md := make(map[string]string, len(e.Metadata))
	for k, v := range e.Metadata {
		if isPrivateMetadata(k) {
			continue
		}
		md[k] = v
	}
	return md
}

// isPrivateMetadata 判断字段是否只供框架内部的错误传输与呈现使用。
func isPrivateMetadata(key string) bool {
	switch key {
	case mdErrStackKey,
		mdReasonCodeKey,
		mdHTTPCodeKey,
		mdHTTPDataKey,
		mdHTTPHeadersKey,
		mdValidationErrorKey:
		return true
	default:
		return false
	}
}

// WithMetadata 返回合并元数据后的错误副本，不修改原错误。
func (e *Error) WithMetadata(md map[string]string) *Error {
	err := clone(e)
	maps.Copy(err.Metadata, md)
	return err
}

// WithErrStack 返回附带调用栈的错误副本，默认从调用点开始记录。
func (e *Error) WithErrStack(optionalSkip ...int) *Error {
	// 默认跳过 runtime.Callers、栈辅助函数和本方法，使首帧直接指向业务调用点。
	var skip = 3
	if len(optionalSkip) > 0 {
		skip = optionalSkip[0]
	}
	err := clone(e)
	err.Metadata[mdErrStackKey] += fmt.Sprintf("%+v", callers(skip))
	return err
}

// ErrStack 返回已保存的调用栈和底层原因。
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

// WithReasonCode 返回携带业务错误码的错误副本。
func (e *Error) WithReasonCode(reasonCode int) *Error {
	err := clone(e)
	err.Metadata[mdReasonCodeKey] = strconv.Itoa(reasonCode)
	return err
}

// ReasonCode 返回业务错误码；未设置或格式无效时回退到 HTTP 状态码。
func (e *Error) ReasonCode() int {
	if e == nil {
		return http.StatusOK
	}
	if e.Metadata == nil || e.Metadata[mdReasonCodeKey] == "" {
		return int(e.Code)
	}
	reasonCode, convErr := strconv.Atoi(e.Metadata[mdReasonCodeKey])
	if convErr != nil {
		return int(e.Code)
	}
	return reasonCode
}

// WithHTTPData 返回携带 HTTP 响应数据快照的错误副本。
func (e *Error) WithHTTPData(data any) *Error {
	err := clone(e)
	err.httpData = cloneHTTPData(data)
	delete(err.Metadata, mdHTTPDataKey)
	return err
}

// HTTPData 返回 HTTP 错误响应数据的独立副本。
func (e *Error) HTTPData() any {
	if e == nil {
		return nil
	}
	return cloneHTTPData(e.httpData)
}

// cloneHTTPData 通过 JSON 边界复制可传输数据；不可编码值保留给编码器统一报错。
func cloneHTTPData(data any) any {
	// 普通错误通常没有附加数据，无需为 nil 构建 JSON 编解码器。
	if data == nil {
		return nil
	}
	encoded, marshalErr := json.Marshal(data)
	if marshalErr != nil {
		return data
	}
	var snapshot any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	// 保留数字原文，避免大整数和高精度小数在复制时被 float64 舍入。
	decoder.UseNumber()
	if unmarshalErr := decoder.Decode(&snapshot); unmarshalErr != nil {
		return data
	}
	return snapshot
}

// WithHTTPHeaders 返回合并 HTTP 响应头后的错误副本，并去除重复值。
func (e *Error) WithHTTPHeaders(headers http.Header) *Error {
	err := clone(e)
	mergedHeaders := err.HTTPHeaders()
	for k, v := range headers {
		for _, vv := range v {
			if !slices.Contains(mergedHeaders.Values(k), vv) {
				mergedHeaders.Add(k, vv)
			}
		}
	}
	jsonData, _ := json.Marshal(mergedHeaders)
	err.Metadata[mdHTTPHeadersKey] = string(jsonData)
	return err
}

// HTTPHeaders 返回 HTTP 错误响应头副本；缺失或损坏时返回空集合。
func (e *Error) HTTPHeaders() http.Header {
	if e == nil || e.Metadata == nil || e.Metadata[mdHTTPHeadersKey] == "" {
		return http.Header{}
	}
	headers := http.Header{}
	_ = json.Unmarshal([]byte(e.Metadata[mdHTTPHeadersKey]), &headers)
	return headers
}

// WithValidationError 返回携带结构化参数校验失败信息的错误副本。
func (e *Error) WithValidationError(validationError []*ValidationError) *Error {
	err := clone(e)

	data, _ := json.Marshal(validationError)
	err.Metadata[mdValidationErrorKey] = string(data)
	return err
}

// ValidationError 返回错误中携带的结构化参数校验失败信息。
func (e *Error) ValidationError() []*ValidationError {
	if e == nil || e.Metadata == nil || e.Metadata[mdValidationErrorKey] == "" {
		return nil
	}

	var errs []*ValidationError
	_ = json.Unmarshal([]byte(e.Metadata[mdValidationErrorKey]), &errs)
	return errs
}

// ErrStack 返回错误链中保存的调用栈和原因。
func ErrStack(err error) string {
	if err == nil {
		return ""
	}
	return FromError(err).ErrStack()
}

// ReasonCode 返回错误链中的业务错误码；nil 表示成功。
func ReasonCode(err error) int {
	if err == nil {
		return 200
	}
	return FromError(err).ReasonCode()
}

// HTTPData 返回错误链携带的 HTTP 响应数据。
func HTTPData(err error) any {
	if err == nil {
		return nil
	}
	return FromError(err).HTTPData()
}

// HTTPHeaders 返回错误链携带的 HTTP 响应头。
func HTTPHeaders(err error) http.Header {
	if err == nil {
		return http.Header{}
	}
	return FromError(err).HTTPHeaders()
}
