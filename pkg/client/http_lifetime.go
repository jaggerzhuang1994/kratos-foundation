package client

import (
	"context"
	"io"
	"net/http"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

// withRequestLifetime 使资源关闭同时取消 Invoke、Do 和响应体读取，不重放请求。
// 取消函数必须保留到 Body.Close；RoundTrip 返回只表示收到响应头，不能提前取消正文。
func withRequestLifetime(lifetime context.Context, transport http.RoundTripper) http.RoundTripper {
	return roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if err := lifetime.Err(); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(request.Context())
		stop := context.AfterFunc(lifetime, cancel)
		release := func() { stop(); cancel() }
		response, err := transport.RoundTrip(request.WithContext(ctx))
		if err != nil {
			release()
			return response, err
		}
		if response.Body == nil {
			release()
			return response, nil
		}
		body := &lifetimeBody{ReadCloser: response.Body, release: release}
		// 协议升级响应可能支持双向读写；包装不能丢失底层已有的 Writer 契约。
		if writer, ok := response.Body.(io.Writer); ok {
			response.Body = struct {
				*lifetimeBody
				io.Writer
			}{body, writer}
		} else {
			response.Body = body
		}
		return response, nil
	})
}

type lifetimeBody struct {
	io.ReadCloser
	release func()
}

func (body *lifetimeBody) Close() error {
	body.release()
	return body.ReadCloser.Close()
}
