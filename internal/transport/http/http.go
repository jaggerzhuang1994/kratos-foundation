package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	nethttp "net/http"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
)

type errResponse struct {
	// Code 保存业务原因码，区别于 HTTP 响应状态码。
	Code int `json:"code"`
	// Message 保存对外展示的错误消息。
	Message string `json:"message"`
	// Data 保存错误附加数据；未指定时可使用校验错误明细。
	Data any `json:"data"`
	// Reason 保存稳定错误原因标识。
	Reason string `json:"reason"`
	// Metadata 保留完整错误元数据，公开出口由网关统一过滤内部字段。
	Metadata map[string]string `json:"metadata"`
}

// Encoder 返回标准 HTTP 服务端错误编码器；调用方必须显式挂载，不修改全局行为。
func Encoder() kratoshttp.EncodeErrorFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
		se := errors.FromError(errors.Normalize(err))
		if se == nil {
			se = errors.New(nethttp.StatusInternalServerError, "UNKNOWN", nethttp.StatusText(nethttp.StatusInternalServerError))
		}
		for k, vv := range se.HTTPHeaders() {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}

		httpData := se.HTTPData()
		validationErr := se.ValidationError()
		if len(validationErr) > 0 && httpData == nil {
			httpData = validationErr
		}

		rsp := errResponse{
			Code:     se.ReasonCode(),
			Message:  se.Message,
			Data:     httpData,
			Reason:   se.Reason,
			Metadata: compatibilityMetadata(se, httpData),
		}

		statusCode := int(se.Code)
		body, marshalErr := json.Marshal(rsp)
		if statusCode < 100 || statusCode > 599 || marshalErr != nil {
			body = []byte(internalServerErrorBody)
			statusCode = nethttp.StatusInternalServerError
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(statusCode)
		_, _ = w.Write(body)
	}
}

const (
	legacyHTTPDataMetadataKey   = "http_data"
	legacyHTTPHeaderMetadataKey = "http_header"
)

// compatibilityMetadata 保留完整内部元数据，并为只解析 metadata 的 v1 HTTP 客户端
// 回填 data 和响应头。返回独立副本，避免编码过程修改业务错误。
func compatibilityMetadata(se *errors.Error, httpData any) map[string]string {
	metadata := maps.Clone(se.Metadata)
	if metadata == nil {
		metadata = make(map[string]string, 2)
	}
	if _, exists := metadata[legacyHTTPDataMetadataKey]; !exists && httpData != nil {
		if encoded, err := json.Marshal(httpData); err == nil {
			metadata[legacyHTTPDataMetadataKey] = string(encoded)
		}
	}
	if _, exists := metadata[legacyHTTPHeaderMetadataKey]; !exists {
		if headers := se.HTTPHeaders(); len(headers) > 0 {
			// 旧 http_header 契约只支持单值；完整多值仍保留在 v2 的 http_headers 中。
			legacyHeaders := make(map[string]string, len(headers))
			for key := range headers {
				legacyHeaders[key] = headers.Get(key)
			}
			if encoded, err := json.Marshal(legacyHeaders); err == nil {
				metadata[legacyHTTPHeaderMetadataKey] = string(encoded)
			}
		}
	}
	return metadata
}

const internalServerErrorBody = `{"code":500,"message":"Internal Server Error","data":null,"reason":"UNKNOWN","metadata":{}}`

const maxErrorBodySize = 1 << 20

// Decoder 返回标准 HTTP 客户端错误解码器；非成功响应最多读取 1 MiB + 1 字节并关闭响应体。
func Decoder() kratoshttp.DecodeErrorFunc {
	return func(ctx context.Context, res *nethttp.Response) error {
		if res.StatusCode >= 200 && res.StatusCode <= 299 {
			return nil
		}
		defer res.Body.Close()

		// 以实际读取量判定上限，兼容 chunked 或不可信的 Content-Length。
		data, err := io.ReadAll(io.LimitReader(res.Body, maxErrorBodySize+1))
		if len(data) > maxErrorBodySize {
			return errors.New(res.StatusCode, "", res.Status).
				WithCause(fmt.Errorf("http error response body exceeds %d bytes", maxErrorBodySize))
		}
		if err == nil {
			// data 先保留原始 JSON，再由 WithHTTPData 构造精确保留数字的快照。
			errRsp := &errResponse{Data: new(json.RawMessage)}
			if err = json.Unmarshal(data, errRsp); err == nil {
				se := errors.New(res.StatusCode, errRsp.Reason, errRsp.Message).
					WithMetadata(errRsp.Metadata).
					WithReasonCode(errRsp.Code).
					WithHTTPHeaders(res.Header)
				if errRsp.Data != nil {
					se = se.WithHTTPData(errRsp.Data)
				}
				return se
			}
		}
		return errors.New(res.StatusCode, "", res.Status).WithCause(err)
	}
}
