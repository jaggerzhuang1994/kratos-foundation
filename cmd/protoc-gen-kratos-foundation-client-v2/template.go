package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"text/template"

	"google.golang.org/protobuf/compiler/protogen"
)

//go:embed template.tpl
var clientTemplate string

// fileDesc 是单个 Proto 文件渲染模板所需的最小数据。
type fileDesc struct {
	// File 保存源文件描述符，供模板读取 Proto 信息。
	*protogen.File
	// Path 是源 Proto 文件路径。
	Path string
	// ProtocGenGoClientRelease 是当前生成器版本。
	ProtocGenGoClientRelease string
	// ProtocVersion 是发起请求的 protoc 版本。
	ProtocVersion string
	// Deprecated 表示源 Proto 文件是否已弃用。
	Deprecated bool
	// ServiceName 是未显式声明 client_name 时使用的默认连接名。
	ServiceName string
	// Services 按 Proto 声明顺序保存服务。
	Services []*serviceDesc
	// Symbols 保存 protogen 解析后的 Go 标识符，以兼容导入别名。
	Symbols goSymbols
}

// goSymbols 保存带正确导入别名的 Go 标识符。
type goSymbols struct {
	// Context 为 context.Context 的限定类型名。
	Context string
	// Errorf 为 fmt.Errorf 的限定函数名。
	Errorf string
	// WireNewSet 为 Wire provider set 构造函数的限定名称。
	WireNewSet string
	// ClientFactory 为客户端工厂的限定类型名。
	ClientFactory string
	// HTTPCallOptions 为从 Context 读取 HTTP 调用选项的限定函数名。
	HTTPCallOptions string
	// GRPCCallOptions 为从 Context 读取 gRPC 调用选项的限定函数名。
	GRPCCallOptions string
}

// serviceDesc 描述一个待生成的非流式服务客户端。
type serviceDesc struct {
	// Service 保存源服务描述符。
	*protogen.Service
	// ServiceName 为服务的 Go 名称，用于拼接生成标识符。
	ServiceName string
	// ServiceFullName 为包含 Proto 包名的服务全限定名。
	ServiceFullName string
	// ClientName 为选择客户端连接配置的名称。
	ClientName string
	// Methods 按声明顺序保存待生成的一元 RPC。
	Methods []*methodDesc
	// Deprecated 表示源服务是否已弃用。
	Deprecated bool
}

// methodDesc 描述一个待生成的一元 RPC 方法。
type methodDesc struct {
	// Method 保存源 RPC 方法描述符。
	*protogen.Method
	// ServiceName 为所属服务的 Go 名称。
	ServiceName string
	// Name 为生成的 Go 方法名称。
	Name string
	// Request 为请求消息的限定 Go 类型名。
	Request string
	// Reply 为响应消息的限定 Go 类型名。
	Reply string
	// Comment 为源 RPC 的文档注释。
	Comment string
	// HasHTTPRule 表示方法是否声明 HTTP 映射规则。
	HasHTTPRule bool
}

// execute 渲染一个 Proto 文件的客户端代码，并将模板错误交给 protoc 报告。
func (fd *fileDesc) execute() (string, error) {
	buf := new(bytes.Buffer)
	tmpl, err := template.New("client").Parse(strings.TrimSpace(clientTemplate))
	if err != nil {
		return "", fmt.Errorf("parse client template: %w", err)
	}

	if err := tmpl.Execute(buf, fd); err != nil {
		return "", fmt.Errorf("execute client template: %w", err)
	}
	return strings.Trim(buf.String(), "\r\n"), nil
}
