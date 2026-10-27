// Package servicename 定义 proto service 到逻辑服务名的唯一推导规则。
//
// 服务名只由 proto 文件路径决定，不提供声明式覆盖：生成期（客户端插件）与运行期（服务注册、网关）
// 共用这套规则，保证客户端解析、服务端注册与网关转发使用同一个名字。
package servicename

import (
	"fmt"
	"go/token"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Layout 是 proto 文件的目录约定，出现在错误信息里帮助定位。
const Layout = "api/<service_name>/*.proto"

// versionName 匹配 v1、v2beta1、v1alpha、v1p1beta1 等 API 版本名；
// proto 放在版本目录下时，目录名不能作为服务名。
var versionName = regexp.MustCompile(`^v[0-9]+(?:(?:alpha|beta)[0-9]*|p[0-9]+)*$`)

// frameworkServices 是 gRPC 标准与框架自带的服务，它们不属于任何业务服务名。
var frameworkServices = map[protoreflect.FullName]struct{}{
	"grpc.health.v1.Health":                    {},
	"grpc.reflection.v1.ServerReflection":      {},
	"grpc.reflection.v1alpha.ServerReflection": {},
	"grpc.channelz.v1.Channelz":                {},
}

// Of 返回运行期服务描述符对应的服务名。
func Of(service protoreflect.ServiceDescriptor) (string, error) {
	if service == nil {
		return "", fmt.Errorf("service descriptor is nil")
	}
	file := service.ParentFile()
	if file == nil {
		return "", fmt.Errorf("service %s: parent file is nil", service.FullName())
	}
	name, err := FromPath(file.Path(), goPackageName(file))
	if err != nil {
		return "", fmt.Errorf("service %s: %w", service.FullName(), err)
	}
	return name, nil
}

// FromPath 按 proto 所在目录名、文件名（顶层文件）、Go 包名的顺序推导服务名；
// 结果是 v1 这类 API 版本名时返回错误，因为它无法区分服务。
// 生成期与运行期都调用它，protoPath 是 protoc 看到的 import 路径。
func FromPath(protoPath, goPackageName string) (string, error) {
	name := path.Base(path.Dir(protoPath))
	if name == "." {
		name = strings.TrimSuffix(path.Base(protoPath), path.Ext(protoPath))
	}
	if name == "" || name == "." || name == "/" {
		name = goPackageName
	}
	if name == "" {
		return "", fmt.Errorf("proto %q: cannot derive service name; follow layout %s", protoPath, Layout)
	}
	if versionName.MatchString(name) {
		return "", fmt.Errorf("proto %q: derived service name %q is an API version; move it to %s", protoPath, name, Layout)
	}
	return name, nil
}

// IsFramework 报告服务是否为 gRPC 标准或框架自带服务（健康检查、反射等），这类服务不参与业务服务名推导。
func IsFramework(service protoreflect.FullName) bool {
	_, ok := frameworkServices[service]
	return ok
}

// goPackageName 按 protoc-gen-go 的规则从 go_package 推导 Go 包名，仅在目录名和文件名都不可用时作为兜底。
func goPackageName(file protoreflect.FileDescriptor) string {
	options, ok := file.Options().(*descriptorpb.FileOptions)
	if !ok || options.GetGoPackage() == "" {
		return sanitizeGoIdent(path.Base(strings.TrimSuffix(file.Path(), path.Ext(file.Path()))))
	}
	goPackage := options.GetGoPackage()
	if index := strings.LastIndexByte(goPackage, ';'); index >= 0 {
		return sanitizeGoIdent(goPackage[index+1:])
	}
	return sanitizeGoIdent(path.Base(goPackage))
}

// sanitizeGoIdent 与 protogen 的 GoSanitized 一致：非法字符替换为下划线，数字开头或关键字前加下划线。
func sanitizeGoIdent(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '_'
	}, name)
	r, _ := utf8.DecodeRuneInString(name)
	if token.Lookup(name).IsKeyword() || !unicode.IsLetter(r) {
		return "_" + name
	}
	return name
}
