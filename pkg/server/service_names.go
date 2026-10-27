package server

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/go-kratos/kratos/v2/transport"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/servicename"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// serviceNames 是业务 Runtime 在组装期收集的服务名，形状与 app.ServiceNamer 一致。
// 推导失败不在组装期中断启动，由服务注册按注册模式决定是告警还是失败。
type serviceNames struct {
	// names 由 proto 推导出的服务名，去重并排序。
	names []string
	// unmapped 无法推导服务名的业务入口描述，排序后用于告警。
	unmapped []string
	// err 服务名推导失败的合并错误。
	err error
}

// ServiceNames 返回收集结果的独立副本。
func (s serviceNames) ServiceNames() ([]string, []string, error) {
	return slices.Clone(s.names), slices.Clone(s.unmapped), s.err
}

// nameCollector 累积一个传输层上的服务名、未映射入口和推导错误。
type nameCollector struct {
	names    map[string]struct{}
	unmapped []string
	errs     []error
}

func (c *nameCollector) add(name string) {
	if c.names == nil {
		c.names = make(map[string]struct{})
	}
	c.names[name] = struct{}{}
}

func (c *nameCollector) result() serviceNames {
	names := make([]string, 0, len(c.names))
	for name := range c.names {
		names = append(names, name)
	}
	sort.Strings(names)
	unmapped := slices.Clone(c.unmapped)
	sort.Strings(unmapped)
	return serviceNames{names: names, unmapped: unmapped, err: errors.Join(c.errs...)}
}

// collectGRPCServiceNames 按已注册的 gRPC 服务推导服务名，跳过健康检查、反射等框架服务；
// 返回值的第二项是 gRPC 服务全名到服务名的映射，用于路由日志。
func collectGRPCServiceNames(srv GRPCServer, files *protoregistry.Files) (serviceNames, map[string]string) {
	var collector nameCollector
	byService := make(map[string]string)
	for service := range srv.GetServiceInfo() {
		fullName := protoreflect.FullName(service)
		if servicename.IsFramework(fullName) {
			continue
		}
		descriptor, err := files.FindDescriptorByName(fullName)
		serviceDescriptor, ok := descriptor.(protoreflect.ServiceDescriptor)
		if err != nil || !ok {
			collector.unmapped = append(collector.unmapped, fmt.Sprintf("grpc service %s has no registered proto descriptor", service))
			continue
		}
		name, err := servicename.Of(serviceDescriptor)
		if err != nil {
			collector.errs = append(collector.errs, err)
			continue
		}
		collector.add(name)
		byService[service] = name
	}
	return collector.result(), byService
}

// httpRoute 是一条 HTTP 路由的方法与 mux 路径模板。
type httpRoute struct {
	method string
	path   string
}

// collectHTTPServiceNames 把业务 HTTP 路由与 proto 中的 google.api.http 规则匹配并推导服务名。
// 匹配不到或匹配到多个服务名的业务路由记为未映射；业务登记过路由却一条都枚举不到时，说明只用了
// Kratos 原生 Handle/HandleFunc/HandlePrefix（mux 不记录方法，WalkRoute 无法枚举），整体记为未映射。
// 返回值的第二项是路由到服务名的映射，用于路由日志。
func collectHTTPServiceNames(srv HTTPServer, pathPrefix string, registered bool, index httpRouteIndex) (serviceNames, map[httpRoute]string, error) {
	var collector nameCollector
	byRoute := make(map[httpRoute]string)
	visible := 0
	err := srv.WalkRoute(func(info http.RouteInfo) error {
		visible++
		route := httpRoute{method: info.Method, path: info.Path}
		names := make(map[string]struct{})
		declared := index.lookup(info.Method, stripPathPrefix(info.Path, pathPrefix))
		for _, descriptor := range declared {
			name, err := servicename.Of(descriptor)
			if err != nil {
				collector.errs = append(collector.errs, err)
				continue
			}
			names[name] = struct{}{}
		}
		switch {
		case len(declared) > 0 && len(names) == 0:
			// 已在 proto 声明但服务名推导失败，错误已记录，不再重复记为未映射。
		case len(names) == 0:
			collector.unmapped = append(collector.unmapped, fmt.Sprintf("http route %s %s is not declared in proto", info.Method, info.Path))
		case len(names) == 1:
			for name := range names {
				collector.add(name)
				byRoute[route] = name
			}
		default:
			ambiguous := make([]string, 0, len(names))
			for name := range names {
				ambiguous = append(ambiguous, name)
			}
			sort.Strings(ambiguous)
			collector.unmapped = append(collector.unmapped, fmt.Sprintf("http route %s %s matches several services %v", info.Method, info.Path, ambiguous))
		}
		return nil
	})
	if err != nil {
		return serviceNames{}, nil, fmt.Errorf("walk HTTP routes: %w", err)
	}
	if visible == 0 && registered {
		collector.unmapped = append(collector.unmapped, "http routes registered through native Kratos APIs cannot be enumerated; use server.HandleRoute")
	}
	return collector.result(), byRoute, nil
}

// stripPathPrefix 去掉 server.http.path_prefix：Kratos 子路由的模板包含前缀，而 proto 规则不包含。
func stripPathPrefix(routePath, prefix string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		return routePath
	}
	stripped, ok := strings.CutPrefix(routePath, prefix)
	if !ok || (stripped != "" && !strings.HasPrefix(stripped, "/")) {
		return routePath
	}
	if stripped == "" {
		return "/"
	}
	return stripped
}

// httpRouteIndex 是 proto 中所有 HTTP 规则到声明它的服务的索引，键与 Kratos 注册到 mux 的路由一致。
type httpRouteIndex map[httpRoute][]protoreflect.ServiceDescriptor

// newHTTPRouteIndex 按 protoc-gen-go-http 的生成规则建立索引：google.api.http 及其 additional_bindings，
// 以及未声明规则的非流式方法的默认路由 POST /<package.Service>/<Method>。流式方法也收录其声明的规则，
// 便于按 proto 声明挂载的 WebSocket 等路由映射到服务名。
func newHTTPRouteIndex(files *protoregistry.Files) httpRouteIndex {
	index := make(httpRouteIndex)
	add := func(method, routePath string, service protoreflect.ServiceDescriptor) {
		key := httpRoute{method: method, path: path.Join("/", kratosRoutePath(routePath))}
		for _, existing := range index[key] {
			if existing.FullName() == service.FullName() {
				return
			}
		}
		index[key] = append(index[key], service)
	}
	files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		services := file.Services()
		for i := range services.Len() {
			service := services.Get(i)
			methods := service.Methods()
			for j := range methods.Len() {
				method := methods.Get(j)
				streaming := method.IsStreamingClient() || method.IsStreamingServer()
				defaultPath := "/" + string(service.FullName()) + "/" + string(method.Name())
				rule, _ := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
				if rule == nil {
					if !streaming {
						add(nethttp.MethodPost, defaultPath, service)
					}
					continue
				}
				for _, binding := range append(slices.Clone(rule.GetAdditionalBindings()), rule) {
					verb, routePath := httpRulePattern(binding)
					if routePath == "" {
						routePath = defaultPath
					}
					add(verb, routePath, service)
				}
			}
		}
		return true
	})
	return index
}

func (i httpRouteIndex) lookup(method, routePath string) []protoreflect.ServiceDescriptor {
	return i[httpRoute{method: method, path: routePath}]
}

// httpRulePattern 与 protoc-gen-go-http 的 buildHTTPRule 一致：未声明方法时使用 POST。
func httpRulePattern(rule *annotations.HttpRule) (string, string) {
	switch pattern := rule.GetPattern().(type) {
	case *annotations.HttpRule_Get:
		return nethttp.MethodGet, pattern.Get
	case *annotations.HttpRule_Put:
		return nethttp.MethodPut, pattern.Put
	case *annotations.HttpRule_Post:
		return nethttp.MethodPost, pattern.Post
	case *annotations.HttpRule_Delete:
		return nethttp.MethodDelete, pattern.Delete
	case *annotations.HttpRule_Patch:
		return nethttp.MethodPatch, pattern.Patch
	case *annotations.HttpRule_Custom:
		if kind := pattern.Custom.GetKind(); kind != "" {
			return kind, pattern.Custom.GetPath()
		}
		return nethttp.MethodPost, pattern.Custom.GetPath()
	default:
		return nethttp.MethodPost, ""
	}
}

var pathVariable = regexp.MustCompile(`(?i){([a-z.0-9_\s]*)=?([^{}]*)}`)

// kratosRoutePath 与 protoc-gen-go-http 的 buildPathVars/replacePath 一致：
// 带模板的变量 {name=messages/*} 转为 mux 正则形式 {name:messages/.*}，名字只有一个字符时保持原样。
func kratosRoutePath(routePath string) string {
	for _, match := range pathVariable.FindAllStringSubmatch(routePath, -1) {
		name := strings.TrimSpace(match[1])
		if len(name) <= 1 || match[2] == "" {
			continue
		}
		pattern := regexp.MustCompile(fmt.Sprintf(`(?i){([\s]*%s\b[\s]*)=?([^{}]*)}`, name))
		if location := pattern.FindStringIndex(routePath); len(location) > 0 {
			routePath = fmt.Sprintf("%s{%s:%s}%s", routePath[:location[0]], name, strings.ReplaceAll(match[2], "*", ".*"), routePath[location[1]:])
		}
	}
	return routePath
}

// serviceRuntime 为业务 Server 附加服务名汇报能力，同时保留端点查询与启动前回滚。
type serviceRuntime struct {
	// Server 被委托启停的业务服务。
	transport.Server
	// Endpointer 保留底层服务的端点查询能力。
	transport.Endpointer
	// serviceNames 组装期收集的服务名。
	serviceNames
}

// withServiceNames 包装实现了 Endpointer 的业务 Server；没有端点的 Server 不参与服务注册，原样返回。
func withServiceNames(server transport.Server, names serviceNames) transport.Server {
	endpointer, ok := server.(transport.Endpointer)
	if !ok {
		return server
	}
	return &serviceRuntime{Server: server, Endpointer: endpointer, serviceNames: names}
}

// AbortStartup 透传启动前回滚。
func (s *serviceRuntime) AbortStartup(ctx context.Context) error {
	if runtime, ok := s.Server.(interface{ AbortStartup(context.Context) error }); ok {
		return runtime.AbortStartup(ctx)
	}
	return nil
}
