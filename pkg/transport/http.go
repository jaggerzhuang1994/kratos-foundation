package transport

import (
	"context"
	"io"
	http2 "net/http"

	"github.com/go-kratos/kratos/v2/encoding/json"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/errors"
)

func init() {
	// http json输出约定
	json.MarshalOptions.AllowPartial = true
	json.MarshalOptions.UseEnumNumbers = true
	json.MarshalOptions.EmitUnpopulated = true
	json.MarshalOptions.EmitDefaultValues = true

	json.UnmarshalOptions.AllowPartial = true
	json.UnmarshalOptions.DiscardUnknown = true
}

// 错误响应
type errResponse struct {
	Code     int               `json:"code"`
	Message  string            `json:"message"`
	Data     any               `json:"data"`
	Reason   string            `json:"reason"`
	Metadata map[string]string `json:"metadata"`
}

// HttpErrorEncoder http服务器如何输出错误
func HttpErrorEncoder() http.EncodeErrorFunc {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		se := errors.FromError(err)
		for k, vv := range se.HttpHeaders() {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}

		w.WriteHeader(int(se.Code))
		httpData := se.HttpData()
		validationErr := se.ValidationError()
		if len(validationErr) > 0 && httpData == nil {
			httpData = validationErr
		}

		rsp := errResponse{
			Code:     se.ReasonCode(),
			Message:  se.Message,
			Data:     httpData,
			Reason:   se.Reason,
			Metadata: se.Metadata,
		}

		_ = http.DefaultResponseEncoder(w, r, rsp)
	}
}

// HttpErrorDecoder http客户端怎么从响应恢复错误
func HttpErrorDecoder() http.DecodeErrorFunc {
	return func(ctx context.Context, res *http2.Response) error {
		if res.StatusCode >= 200 && res.StatusCode <= 299 {
			return nil
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err == nil {
			errRsp := new(errResponse)
			if err = http.CodecForResponse(res).Unmarshal(data, errRsp); err == nil {
				return errors.New(res.StatusCode, errRsp.Reason, errRsp.Message).WithMetadata(errRsp.Metadata)
			}
		}
		return errors.Newf(res.StatusCode, "", "").WithCause(err)
	}
}
