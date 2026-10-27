package server

import (
	"fmt"
	nethttp "net/http"
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// protoHTTPMethod 是按方法全名解析出的 proto 方法及其 google.api.http 路由。
type protoHTTPMethod struct {
	// operation 是 /<package.Service>/<Method> 形式的接口名，与生成代码一致。
	operation string
	// descriptor 是方法描述符。
	descriptor protoreflect.MethodDescriptor
	// routes 是 google.api.http 及其 additional_bindings 对应的 mux 路由。
	routes []httpRoute
}

// resolveProtoHTTPMethod 按 /<package.Service>/<Method>（gRPC 生成的 *_FullMethodName）解析方法与 HTTP 规则；
// 也接受省略开头斜杠的写法。方法不存在或没有 google.api.http 时返回错误。
func resolveProtoHTTPMethod(files *protoregistry.Files, fullMethod string) (protoHTTPMethod, error) {
	trimmed := strings.TrimPrefix(fullMethod, "/")
	serviceName, methodName, ok := strings.Cut(trimmed, "/")
	if !ok || serviceName == "" || methodName == "" || strings.Contains(methodName, "/") {
		return protoHTTPMethod{}, fmt.Errorf("proto method %q must be /<package.Service>/<Method>", fullMethod)
	}
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(serviceName))
	if err != nil {
		return protoHTTPMethod{}, fmt.Errorf("proto method %q: service %s is not registered: %w", fullMethod, serviceName, err)
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return protoHTTPMethod{}, fmt.Errorf("proto method %q: %s is not a service", fullMethod, serviceName)
	}
	method := service.Methods().ByName(protoreflect.Name(methodName))
	if method == nil {
		return protoHTTPMethod{}, fmt.Errorf("proto method %q: service %s has no method %s", fullMethod, serviceName, methodName)
	}
	rule, _ := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule == nil {
		return protoHTTPMethod{}, fmt.Errorf("proto method %q has no google.api.http rule", fullMethod)
	}
	resolved := protoHTTPMethod{operation: "/" + serviceName + "/" + methodName, descriptor: method}
	for _, binding := range append([]*annotations.HttpRule{rule}, rule.GetAdditionalBindings()...) {
		verb, routePath := httpRulePattern(binding)
		if routePath == "" {
			return protoHTTPMethod{}, fmt.Errorf("proto method %q declares an HTTP rule without a path", fullMethod)
		}
		resolved.routes = append(resolved.routes, httpRoute{method: verb, path: kratosRoutePath(routePath)})
	}
	return resolved, nil
}

// streaming 报告方法是否为客户端或服务端流式。
func (m protoHTTPMethod) streaming() bool {
	return m.descriptor.IsStreamingClient() || m.descriptor.IsStreamingServer()
}

// HandleMethod 把手写处理器挂到 proto 方法声明的 HTTP 路由上：method 与 path 取自 google.api.http
// （含 additional_bindings），operation 为方法全名，行为与 HandleRoute 一致。fullMethod 传 gRPC 生成的
// *_FullMethodName 常量，例如 orderspb.OrderService_Cancel_FullMethodName。
//
// 路由与 proto 声明保持一致，服务注册据此把它归入对应服务名。方法不存在、没有 google.api.http 或是流式方法
// （WebSocket 使用 HTTPBuilder.WebSocketMethod）时注册失败，Server 组装返回错误。handler 为 nil 时返回 nil。
func HandleMethod(fullMethod string, handler HTTPRouteHandler) HTTPEndpoint {
	if handler == nil {
		return nil
	}
	return func(server HTTPServer) error {
		method, err := resolveProtoHTTPMethod(protoregistry.GlobalFiles, fullMethod)
		if err != nil {
			return err
		}
		if method.streaming() {
			return fmt.Errorf("proto method %q is streaming; mount WebSocket endpoints with WebSocketMethod", fullMethod)
		}
		for _, route := range method.routes {
			if err := HandleRoute(route.method, route.path, method.operation, handler)(server); err != nil {
				return err
			}
		}
		return nil
	}
}

// resolveWebSocketMethod 解析按 proto 声明挂载的 WebSocket 端点：方法须为流式（protoc-gen-go-http 跳过流式方法，
// 不会生成冲突的 HTTP 处理器），且只声明一条 GET 规则（WebSocket 握手只用 GET）。
func resolveWebSocketMethod(files *protoregistry.Files, fullMethod string) (protoHTTPMethod, error) {
	method, err := resolveProtoHTTPMethod(files, fullMethod)
	if err != nil {
		return protoHTTPMethod{}, err
	}
	if !method.streaming() {
		return protoHTTPMethod{}, fmt.Errorf("websocket proto method %q must be a streaming rpc so protoc-gen-go-http skips it", fullMethod)
	}
	if len(method.routes) != 1 || method.routes[0].method != nethttp.MethodGet {
		return protoHTTPMethod{}, fmt.Errorf("websocket proto method %q must declare exactly one GET google.api.http rule", fullMethod)
	}
	return method, nil
}

// websocketGRPCConflicts 列出按 proto 挂载的 WebSocket 方法中，其所属服务同时注册到 gRPC 的条目：
// gRPC 上会暴露一个没有实现的流式方法，通常是误把只用于声明的服务注册到了 gRPC。
func websocketGRPCConflicts(spec *Spec, grpcServer GRPCServer) []string {
	if grpcServer == nil {
		return nil
	}
	services := grpcServer.GetServiceInfo()
	var conflicts []string
	for _, registration := range spec.http.registrations {
		endpoint := registration.websocket
		if endpoint == nil || endpoint.method.descriptor == nil {
			continue
		}
		service := string(endpoint.method.descriptor.Parent().FullName())
		if _, ok := services[service]; ok && !slices.Contains(conflicts, endpoint.method.operation) {
			conflicts = append(conflicts, endpoint.method.operation)
		}
	}
	return conflicts
}
