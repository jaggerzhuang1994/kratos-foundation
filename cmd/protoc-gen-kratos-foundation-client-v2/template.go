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
	Context         string
	Errorf          string
	WireNewSet      string
	ClientFactory   string
	HTTPCallOptions string
	GRPCCallOptions string
}

// serviceDesc 描述一个待生成的非流式服务客户端。
type serviceDesc struct {
	*protogen.Service
	ServiceName     string
	ServiceFullName string
	ClientName      string
	Methods         []*methodDesc
	Deprecated      bool
}

// methodDesc 描述一个待生成的一元 RPC 方法。
type methodDesc struct {
	*protogen.Method
	ServiceName string
	Name        string
	Request     string
	Reply       string
	Comment     string
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
