package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

// 请求体上限：声明长度超限直接 413；分块传输经 Bind 或业务读取超限也返回 413；按路由放大与 0 不限；WebSocket 升级跳过。
func TestRequestBodyLimits(t *testing.T) {
	spec := NewSpec()
	spec.HTTP().Register(func(srv HTTPServer) error {
		route := srv.Route("/")
		bind := func(ctx kratoshttp.Context) error {
			var body map[string]any
			if err := ctx.Bind(&body); err != nil {
				return err
			}
			return ctx.Result(http.StatusOK, body)
		}
		route.POST("/bind", bind)
		route.POST("/upload/file", bind)
		route.POST("/raw", bind)
		return nil
	}, HandleHTTP(http.MethodPost, "/read", func(r *http.Request) (any, error) {
		_, err := io.ReadAll(r.Body)
		return map[string]string{}, err
	}))
	config := &config_pb.Server{Http: &config_pb.HttpServerOption{RequestBody: &config_pb.HttpServerOption_RequestBody{
		MaxBytes: proto.Int64(16),
		Routes: []*config_pb.HttpServerOption_RequestBody_Route{
			{Rule: &config_pb.HttpServerOption_RequestBody_Route_Prefix{Prefix: "/upload/"}, MaxBytes: 1024},
			{Rule: &config_pb.HttpServerOption_RequestBody_Route_Path{Path: "/raw"}, MaxBytes: 0},
		},
	}}}
	server, err := newHTTPServer(config, newHTTPServerOptions(config, nil, spec), spec, newRuntimeTestLogger(t), newWebSocketHub())
	if err != nil {
		t.Fatal(err)
	}
	big := `{"data":"` + strings.Repeat("x", 64) + `"}`
	do := func(path string, chunked bool, headers map[string]string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(big))
		request.Header.Set("Content-Type", "application/json")
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		if chunked {
			request.ContentLength = -1
		}
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, request)
		var reply struct{ Reason string }
		_ = json.Unmarshal(recorder.Body.Bytes(), &reply)
		return recorder.Code, reply.Reason
	}
	for _, tc := range []struct {
		name    string
		path    string
		chunked bool
		headers map[string]string
		code    int
	}{
		{"declared length over limit", "/bind", false, nil, http.StatusRequestEntityTooLarge},
		{"chunked body over limit via Bind", "/bind", true, nil, http.StatusRequestEntityTooLarge},
		{"chunked body over limit read by handler", "/read", true, nil, http.StatusRequestEntityTooLarge},
		{"prefix override allows uploads", "/upload/file", false, nil, http.StatusOK},
		{"zero means unlimited", "/raw", true, nil, http.StatusOK},
		{"websocket upgrade skipped", "/bind", false, map[string]string{"Upgrade": "websocket"}, http.StatusOK},
	} {
		code, reason := do(tc.path, tc.chunked, tc.headers)
		if code != tc.code || (tc.code == http.StatusRequestEntityTooLarge && reason != "REQUEST_BODY_TOO_LARGE") {
			t.Errorf("%s: code=%d reason=%q, want %d", tc.name, code, reason, tc.code)
		}
	}
	if limits := newRequestBodyLimits(nil); limits.limit("/any") != 32<<20 {
		t.Fatalf("default limit = %d", limits.limit("/any"))
	}
	if err := foundationerrors.Normalize(&http.MaxBytesError{Limit: 1}); foundationerrors.Code(err) != http.StatusRequestEntityTooLarge {
		t.Fatalf("MaxBytesError normalized to %v", err)
	}
}
