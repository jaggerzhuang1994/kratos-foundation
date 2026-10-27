package server

import (
	"bytes"
	"errors"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"

	"github.com/go-kratos/kratos/v2/transport/http"
	foundationhttp "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/transport/http"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

// defaultMaxRequestBodyBytes 是业务 HTTP 请求体的默认上限；文件上传等大请求按路由放大。
const defaultMaxRequestBodyBytes int64 = 32 << 20

// requestBodyLimits 按路径解析请求体上限：精确 path 优先，其次最长 prefix，最后全局值；0 表示不限制。
type requestBodyLimits struct {
	global   int64
	paths    map[string]int64
	prefixes []prefixLimit
}

type prefixLimit struct {
	prefix string
	limit  int64
}

func newRequestBodyLimits(config *config_pb.HttpServerOption_RequestBody) requestBodyLimits {
	limits := requestBodyLimits{global: defaultMaxRequestBodyBytes, paths: map[string]int64{}}
	if config != nil && config.MaxBytes != nil {
		limits.global = config.GetMaxBytes()
	}
	for _, route := range config.GetRoutes() {
		switch {
		case route.GetPath() != "":
			limits.paths[route.GetPath()] = route.GetMaxBytes()
		case route.GetPrefix() != "":
			limits.prefixes = append(limits.prefixes, prefixLimit{prefix: route.GetPrefix(), limit: route.GetMaxBytes()})
		}
	}
	return limits
}

func (l requestBodyLimits) limit(path string) int64 {
	if limit, ok := l.paths[path]; ok {
		return limit
	}
	best, limit := -1, l.global
	for _, rule := range l.prefixes {
		if strings.HasPrefix(path, rule.prefix) && len(rule.prefix) > best {
			best, limit = len(rule.prefix), rule.limit
		}
	}
	return limit
}

// filter 在路由前限制请求体：声明的 Content-Length 超限直接 413，分块传输在读取时截断。
// WebSocket 升级请求没有请求体，帧数据经接管的连接传输，不受 MaxBytesReader 影响，仍显式跳过。
func (l requestBodyLimits) filter(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		limit := l.limit(r.URL.Path)
		if limit <= 0 || r.Body == nil || r.Body == nethttp.NoBody || strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			next.ServeHTTP(w, r)
			return
		}
		if r.ContentLength > limit {
			foundationhttp.Encoder()(w, r, requestBodyTooLarge(limit))
			return
		}
		r.Body = nethttp.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

func requestBodyTooLarge(limit int64) error {
	return foundationerrors.New(nethttp.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", "request body too large").
		WithMetadata(map[string]string{"limit_bytes": strconv.FormatInt(limit, 10)})
}

// decodeRequest 包装 Kratos 默认解码：默认实现会把读取超限转成 400 CODEC 并丢掉错误类型，这里先读出正文识别 413。
func decodeRequest(r *nethttp.Request, v any) error {
	if r.Body != nil && r.Body != nethttp.NoBody {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *nethttp.MaxBytesError
			if errors.As(err, &tooLarge) {
				return requestBodyTooLarge(tooLarge.Limit)
			}
			return foundationerrors.New(nethttp.StatusBadRequest, "CODEC", "read request body failed").WithCause(err)
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
	}
	return http.DefaultRequestDecoder(r, v)
}
